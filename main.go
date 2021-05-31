package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/viper"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	_ "github.com/emersion/go-message/charset"
	"github.com/emersion/go-message/mail"
	"github.com/google/uuid"
)

type Config struct {
	Connect struct {
		Host string
		Port int
	}
	Credentials struct {
		Username string
		Password string
	}
	Download struct {
		AttachmentsDirectory string
		PageSize             uint32
		Pattern              string
	}
	Debug bool
}

func DebugPrintHeader(header mail.Header, config Config) {
	if !config.Debug {
		return
	}
	// Print some info about the message
	if date, err := header.Date(); err == nil {
		log.Println("Date:", date)
	}
	if from, err := header.AddressList("From"); err == nil {
		log.Println("From:", from)
	}
	if to, err := header.AddressList("To"); err == nil {
		log.Println("To:", to)
	}
	if subject, err := header.Subject(); err == nil {
		log.Println("Subject:", subject)
	}
}

func SaveAttachmentFromMessage(msg *imap.Message, header *mail.AttachmentHeader, part *mail.Part, config Config) error {
	// This is an attachment
	filename, _ := header.Filename()

	matched, err := regexp.MatchString(config.Download.Pattern, filename)
	if err != nil {
		return fmt.Errorf("failed to check pattern for filename %s: %w", filename, err)
	}

	// We don't care about this type of file, so skip it
	if !matched {
		return nil
	}

	filename = strings.Replace(filename, "/", "_", -1)

	// Not sure why this happens, but we'll just give it a random filename so we can move on
	if filename == "" {
		filename = fmt.Sprintf("unknown-file-%v", uuid.Must(uuid.NewRandom()))
	}

	// Create file with attachment name
	filePath := filepath.Join(config.Download.AttachmentsDirectory, filename)
	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", filePath, err)
	}

	// using io.Copy instead of io.ReadAll to avoid insufficient memory issues
	if _, err := io.Copy(file, part.Body); err != nil {
		return fmt.Errorf("failed to save attachment %s: %w", filename, err)
	}

	if err := os.Chtimes(file.Name(), msg.Envelope.Date, msg.Envelope.Date); err != nil {
		// Log the error but don't fail because of it
		log.Printf("unable to update time on file %s: %+v", file.Name(), err)
	}

	log.Printf("Saved %v\n", filename)
	return nil
}

func ProcessMessage(msg *imap.Message, section imap.BodySectionName, config Config) error {
	// Get the whole message body
	r := msg.GetBody(&section)
	if r == nil {
		log.Fatal("Server didn't returned message body")
	}
	// Create a new mail reader
	mr, err := mail.CreateReader(r)
	if err != nil {
		log.Fatalf("failed to create reader %+v", err)
	}

	DebugPrintHeader(mr.Header, config)

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		} else if err != nil {
			return fmt.Errorf("failed to get next part of message %d: %w", msg.Uid, err)
		}

		switch header := part.Header.(type) {
		case *mail.AttachmentHeader:
			if err := SaveAttachmentFromMessage(msg, header, part, config); err != nil {
				return fmt.Errorf("failed to save attachment: %w", err)
			}
		}
	}
	return nil
}

func DownloadAttachmentsFromPage(client *client.Client, from uint32, to uint32, config Config) error {
	pageRange := new(imap.SeqSet)
	pageRange.AddRange(from, to)

	messages := make(chan *imap.Message, to-from)
	var section imap.BodySectionName

	done := make(chan error, 1)
	go func() {
		done <- client.Fetch(pageRange, []imap.FetchItem{imap.FetchEnvelope, section.FetchItem()}, messages)
	}()

	for msg := range messages {
		if err := ProcessMessage(msg, section, config); err != nil {
			return fmt.Errorf("failed to process message %d: %w", msg.Uid, err)
		}
	}

	if err := <-done; err != nil {
		return fmt.Errorf("failed to fetch messages in range %d - %d: %w", from, to, err)
	}
	return nil
}

func DownloadAttachmentsFromMailbox(client *client.Client, mailbox string, config Config) error {
	mbox, err := client.Select(mailbox, false)
	if err != nil {
		log.Fatal(err)
	}

	pageSize := config.Download.PageSize

	from := uint32(1)
	to := mbox.Messages

	for from <= mbox.Messages {
		to = from + pageSize
		if to > mbox.Messages {
			to = mbox.Messages
		}

		fmt.Printf("Processing %d - %d out of %d\n", from, to, mbox.Messages)

		if err := DownloadAttachmentsFromPage(client, from, to, config); err != nil {
			return fmt.Errorf("an error occurred when downloading attachments: %w", err)
		}

		from = from + pageSize
	}
	return nil
}

func LoadConfig() Config {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("/etc/imapAttachmentDownload/")
	viper.AddConfigPath("$HOME/.imapAttachmentDownload")
	viper.AddConfigPath(".")

	err := viper.ReadInConfig()
	if err != nil {
		panic(fmt.Errorf("Error config file: %s \n", err))
	}

	var options Config

	err = viper.Unmarshal(&options)
	if err != nil {
		panic(fmt.Errorf("Unable to decode Config: %s \n", err))
	}
	return options
}

func main() {
	config := LoadConfig()

	if _, err := os.Stat(config.Download.AttachmentsDirectory); os.IsNotExist(err) {
		if err := os.MkdirAll(config.Download.AttachmentsDirectory, os.ModePerm); err != nil {
			log.Fatalf("failed to create attachments directory %s: %+v", config.Download.AttachmentsDirectory, err)
		}
	}

	log.Println("Connecting to server...")

	// Connect to server
	c, err := client.DialTLS(fmt.Sprintf("%s:%d", config.Connect.Host, config.Connect.Port), nil)
	if err != nil {
		log.Fatal(err)
	}

	log.Println("Connected")

	defer func() {
		if err := c.Logout(); err != nil {
			panic(fmt.Errorf("failed to logout: %w", err))
		}
		log.Println("Logged out")
	}()

	log.Printf("Logging into %s\n", config.Credentials.Username)
	if err := c.Login(config.Credentials.Username, config.Credentials.Password); err != nil {
		log.Fatal(err)
	}
	log.Println("Logged in")

	log.Printf("Attachments Directory %s\n", config.Download.AttachmentsDirectory)
	if err := DownloadAttachmentsFromMailbox(c, "INBOX", config); err != nil {
		log.Fatalf("failed to download attachments: %+v\n", err)
	}

	log.Printf("Finished processing emails. Attachments are in %s\n", config.Download.AttachmentsDirectory)
}

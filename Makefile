OUTPUT_DIR=build
PROJECT_NAME := "download-attachments"
TARGET=$(OUTPUT_DIR)/$(PROJECT_NAME)

build:
	echo "Compiling for every OS and Platform"
	GOOS=linux GOARCH=386 go build -o bin/download-attachments-linux-386 main.go
	GOOS=darwin GOARCH=amd64 go build -o bin/download-attachments-darwin-amd64 main.go
	GOOS=windows GOARCH=386 go build -o bin/download-attachments.exe main.go

all: build
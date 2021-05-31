# Configuration

Create a file called ```config.yml``` in the same directory as your executable
```yaml
connect:
  host: "your-imap-server"
  port: 993
credentials:
  username: "your-email-address"
  password: "your-password"
download:
  attachmentsDirectory: "attachments"
  pageSize: 500
  pattern: '.+.pdf'
debug: false
```
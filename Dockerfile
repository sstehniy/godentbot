# Build Stage
FROM golang:1.21 AS builder

WORKDIR /app
COPY . .
RUN go get -d -v ./...
RUN go build -o go-rod-app .

# Final Stage
FROM alpine:latest

# Update package list and install Chromium
RUN apk add chromium

# Copy the compiled Go application from the build stage
COPY --from=builder /app/go-rod-app /usr/local/bin/go-rod-app

# Set the working directory
WORKDIR /usr/local/bin

# Command to run the application
CMD ["./go-rod-app"]

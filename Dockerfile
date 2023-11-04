FROM golang:1.21 AS builder

ENV GO111MODULE=on 

WORKDIR /app

# manage dependencies
COPY . .
RUN go get -d -v ./...
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o go-rod-app .

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -a -o /go-rod-app .

FROM alpine:latest  
# Install base packages
RUN apk update
RUN apk upgrade
RUN apk add --no-cache chromium
WORKDIR /root/
COPY --from=builder /go-rod-app ./
CMD ["./go-rod-app"]  
FROM golang:1.26.10-alpine

WORKDIR /app

COPY go.mod ./

RUN go mod download
RUN go install github.com/air-verse/air@latest

COPY . .

EXPOSE 9000

CMD ["air"]

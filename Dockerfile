FROM golang:1.27-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o subtitle-translator .

FROM alpine:latest

RUN apk --no-cache add ca-certificates ffmpeg

WORKDIR /app

COPY --from=builder /app/subtitle-translator .

EXPOSE 9595

CMD ["./subtitle-translator", "-server"]

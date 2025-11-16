FROM golang:1.25-alpine AS builder

# Instalar dependencias de compilación
RUN apk add --no-cache git

WORKDIR /app

# Copiar archivos de Go
COPY go.mod go.sum ./
RUN go mod download

# Copiar código fuente
COPY *.go ./

# Compilar la aplicación
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o subtitle-translator .

# Imagen final
FROM alpine:latest

# Instalar solo las dependencias mínimas (ffmpeg viene del host)
RUN apk add --no-cache \
    ca-certificates \
    tzdata \
    libgomp \
    libgcc \
    libstdc++

WORKDIR /app

# Copiar el binario compilado
COPY --from=builder /app/subtitle-translator .

# Ejecutar la aplicación
ENTRYPOINT ["./subtitle-translator"]
CMD ["-watch", "/videos"]

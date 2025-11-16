# Subtitle Translator - Docker Setup

Automated subtitle translation service using HuggingFace and Docker.

## 📋 Requirements

- Docker and Docker Compose installed
- HuggingFace API token
- FFmpeg installed on the host (already available ✅)
- Existing video volumes (already available ✅)

## 🚀 Quick Setup

### 1. Get HuggingFace Token

1. Go to https://huggingface.co/settings/tokens
2. Create a new access token
3. Copy the token

### 2. Configure the Project

```bash
# Create the project directory
mkdir subtitle-translator
cd subtitle-translator

# Create the .env file
nano .env
# Add: HF_TOKEN=your_token_here
```

### 3. Edit docker-compose.yml

**IMPORTANT:** Change the volume paths in `docker-compose.yml`:

```yaml
volumes:
  - /path/to/your/videos:/videos # ⚠️ Replace with your actual path
```

### 4. File Structure

```
subtitle-translator/
├── docker-compose.yml
├── Dockerfile
├── .env
├── main.go
├── go.mod
└── go.sum
```

### 5. Launch the Service

```bash
# Build and run
docker-compose up -d

# View logs
docker-compose logs -f

# Stop the service
docker-compose down
```

## 📄 License

MIT

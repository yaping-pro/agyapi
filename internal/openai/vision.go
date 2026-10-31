package openai

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// MaterializeImages writes any embedded/remote images referenced by the given
// messages into temp files and records their paths on each message's ImageFiles
// field. The returned cleanup func removes every created file.
func MaterializeImages(msgs []ChatMessage) (func(), error) {
	var created []string
	cleanup := func() {
		for _, f := range created {
			os.Remove(f)
		}
	}

	for i := range msgs {
		for _, raw := range msgs[i].Images {
			path, err := persistImage(raw)
			if err != nil {
				cleanup()
				return func() {}, fmt.Errorf("persist image: %w", err)
			}
			created = append(created, path)
			msgs[i].ImageFiles = append(msgs[i].ImageFiles, path)
		}
	}
	return cleanup, nil
}

func persistImage(raw string) (string, error) {
	var data []byte
	var ext string

	switch {
	case strings.HasPrefix(raw, "data:"):
		// data:image/png;base64,XXXX
		comma := strings.Index(raw, ",")
		if comma < 0 {
			return "", fmt.Errorf("malformed data URI")
		}
		meta := raw[len("data:"):comma]
		payload := raw[comma+1:]
		if i := strings.Index(meta, ";"); i >= 0 {
			mime := meta[:i]
			ext = mimeExt(mime)
		}
		if strings.Contains(meta, "base64") {
			b, err := base64.StdEncoding.DecodeString(payload)
			if err != nil {
				return "", fmt.Errorf("base64 decode: %w", err)
			}
			data = b
		} else {
			data = []byte(payload)
		}
	case strings.HasPrefix(raw, "http://"), strings.HasPrefix(raw, "https://"):
		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Get(raw)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("fetch image: status %d", resp.StatusCode)
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		if err != nil {
			return "", err
		}
		data = b
		ext = mimeExt(resp.Header.Get("Content-Type"))
	default:
		// bare base64 payload
		b, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return "", fmt.Errorf("bare base64 decode: %w", err)
		}
		data = b
	}

	if ext == "" {
		ext = detectExt(data)
	}
	if len(data) == 0 {
		return "", fmt.Errorf("empty image payload")
	}

	f, err := os.CreateTemp("", "agyimg-*"+ext)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func mimeExt(mime string) string {
	mime = strings.TrimSpace(strings.ToLower(strings.SplitN(mime, ";", 2)[0]))
	switch mime {
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/heic":
		return ".heic"
	}
	return ""
}

func detectExt(b []byte) string {
	switch {
	case len(b) >= 8 && b[0] == 0x89 && b[1] == 'P' && b[2] == 'N' && b[3] == 'G':
		return ".png"
	case len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF:
		return ".jpg"
	case len(b) >= 6 && string(b[:6]) == "GIF87a":
		return ".gif"
	case len(b) >= 6 && string(b[:6]) == "GIF89a":
		return ".gif"
	case len(b) >= 12 && string(b[0:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return ".webp"
	}
	return ".bin"
}

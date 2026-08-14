package assets

import (
	"embed"
	"encoding/base64"
	"fmt"
	"sync"
)

//go:embed embedded/*
var embeddedFiles embed.FS

var (
	once sync.Once
	ref1 string
	ref2 string
)

func initAssets() {
	ref1 = loadBase64("embedded/image copy.png", "image/png")
	if ref1 == "" {
		ref1 = loadBase64("embedded/image.png", "image/png")
	}

	ref2 = loadBase64("embedded/image.png", "image/png")
	if ref2 == "" {
		ref2 = loadBase64("embedded/image copy.png", "image/png")
	}
}

func loadBase64(path, mimeType string) string {
	data, err := embeddedFiles.ReadFile(path)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(data))
}

// GetJanviRef1Base64 returns Janvi's primary reference image data URL.
func GetJanviRef1Base64() string {
	once.Do(initAssets)
	return ref1
}

// GetJanviRef2Base64 returns Janvi's secondary reference image data URL.
func GetJanviRef2Base64() string {
	once.Do(initAssets)
	return ref2
}

// GetJanviAIReferenceImages returns the array of reference image data URLs.
func GetJanviAIReferenceImages() []string {
	once.Do(initAssets)
	var list []string
	if ref1 != "" {
		list = append(list, ref1)
	}
	if ref2 != "" && ref2 != ref1 {
		list = append(list, ref2)
	}
	return list
}

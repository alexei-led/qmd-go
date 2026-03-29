//go:build !localembed

package provider

import "fmt"

func openVectorizer(_ string) (vectorizer, error) {
	return nil, fmt.Errorf("local embedding not available: rebuild with -tags localembed and ensure libllama_go is installed")
}

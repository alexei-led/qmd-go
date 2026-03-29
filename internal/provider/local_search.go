//go:build localembed

package provider

import "github.com/kelindar/search"

func openVectorizer(path string) (vectorizer, error) {
	return search.NewVectorizer(path, 0)
}

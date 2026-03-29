package provider

import (
	"fmt"
	"os"
)

// ModelStatus describes the state of a model file.
type ModelStatus struct {
	Path   string
	Exists bool
	Size   int64
}

// CheckModel checks whether the configured model file exists.
func CheckModel(configModel string) ModelStatus {
	path := ResolveModelPath(configModel)
	info, err := os.Stat(path)
	if err != nil {
		return ModelStatus{Path: path, Exists: false}
	}
	return ModelStatus{Path: path, Exists: true, Size: info.Size()}
}

// PullModel checks the model and returns guidance if not found.
// Local models must be manually placed; remote providers need no local model.
func PullModel(configModel string, providerType string) (ModelStatus, error) {
	if providerType != "" && providerType != "local" {
		return ModelStatus{}, fmt.Errorf("remote provider %q does not require a local model", providerType)
	}

	status := CheckModel(configModel)
	if status.Exists {
		return status, nil
	}

	return status, fmt.Errorf("model not found at %s\nDownload a GGUF model and place it at that path.\nExample: MiniLM-L6-v2.Q8_0.gguf from Hugging Face", status.Path)
}

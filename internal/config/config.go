package config

import (
	"encoding/json"
	"fmt"
	"os"
)

type Camera struct {
	IP       string `json:"ip"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func (c Camera) Validate() error {
	if c.IP == "" || c.Username == "" || c.Password == "" {
		return fmt.Errorf("camera requires ip, username and password")
	}
	return nil
}

func LoadCameras(path string) ([]Camera, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read camera file: %w", err)
	}

	var cameras []Camera
	if err := json.Unmarshal(data, &cameras); err != nil {
		return nil, fmt.Errorf("parse camera file: %w", err)
	}

	for i, camera := range cameras {
		if err := camera.Validate(); err != nil {
			return nil, fmt.Errorf("camera %d invalid: %w", i, err)
		}
	}

	return cameras, nil
}

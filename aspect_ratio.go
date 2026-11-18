package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
)

type VideoMetaData struct {
	Streams []struct {
		Index     int    `json:"index"`
		CodecName string `json:"codec_name"`
		CodecType string `json:"codec_type"`
		Width     int    `json:"width"`
		Height    int    `json:height"`
	} `json:"streams"`
}

func getVideoAspectRatio(filePath string) (string, error) {
	cmd := exec.Command(
		"ffprobe",
		"-v", "error",
		"-print_format", "json",
		"-show_streams",
		filePath,
	)
	var buffer bytes.Buffer
	cmd.Stdout = &buffer
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("ffprobe error: %v", err)
	}

	var videoMetaData VideoMetaData
	if err := json.Unmarshal(buffer.Bytes(), &videoMetaData); err != nil {
		return "", fmt.Errorf("could not parse ffprobe output: %v", err)
	}

	tolerance := 0.01

	for _, stream := range videoMetaData.Streams {
		if stream.CodecType == "video" {
			if stream.Height > 0 && stream.Width > 0 {
				aspectRatio := float64(stream.Width) / float64(stream.Height)

				if aspectRatio > 1.7778-tolerance && aspectRatio < 1.7778+tolerance {
					return "16:9", nil
				} else if aspectRatio > 0.5625-tolerance && aspectRatio < 0.5625+tolerance {
					return "9:16", nil
				} else {
					return "other", nil
				}
			}
		}
	}

	return "", nil
}

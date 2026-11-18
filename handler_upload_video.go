package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't find video metadata", err)
		return
	}
	if video.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "Not authorized to update this video", nil)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<30) // 1 GB

	videoFile, videoHeader, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Could not parse file", err)
		return
	}
	defer videoFile.Close()

	mediaType := videoHeader.Header.Get("Content-Type")
	if mediaType == "" {
		respondWithError(w, http.StatusBadRequest, "Could not find the Content-Type for video", nil)
		return
	}

	videoType, _, err := mime.ParseMediaType(mediaType)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Could not parse media type", err)
		return
	}
	if videoType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "File must be .mp4 format", nil)
		return
	}

	tempFile, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not create temp file", err)
		return
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	_, err = io.Copy(tempFile, videoFile)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not copy video to temp file", err)
		return
	}

	tempFile.Seek(0, io.SeekStart) // Point to beginning of file

	outputFilePath, err := processVideoForFastStart(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not process video for fast start", err)
		return
	}

	outputFile, err := os.Open(outputFilePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not open processed video", err)
		return
	}
	defer os.Remove(outputFilePath)
	defer outputFile.Close()

	aspectRatio, err := getVideoAspectRatio(outputFilePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not get aspect ratio of video", err)
		return
	}

	var prefix string
	switch aspectRatio {
	case "16:9":
		prefix = "landscape"
	case "9:16":
		prefix = "portrait"
	default:
		prefix = "other"
	}

	fileKey := fmt.Sprintf("%s/%s", prefix, getAssetPath(mediaType))
	_, err = cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:      aws.String(cfg.s3Bucket),
		Key:         aws.String(fileKey),
		Body:        outputFile,
		ContentType: aws.String(mediaType),
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not upload video to storage", err)
		return
	}

	s3VideoURL := cfg.getObjectURL(fileKey)
	video.VideoURL = &s3VideoURL
	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't update video", err)
		return
	}

	respondWithJSON(w, http.StatusOK, video)
}

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

func processVideoForFastStart(filePath string) (string, error) {
	outputFilePath := fmt.Sprintf("%s%s", filePath, ".processing")
	cmd := exec.Command(
		"ffmpeg",
		"-i", filePath,
		"-c", "copy",
		"-movflags", "faststart",
		"-f", "mp4",
		outputFilePath,
	)
	var buffer bytes.Buffer
	cmd.Stdout = &buffer
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("ffmpeg error: %v", err)
	}

	return outputFilePath, nil
}

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
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
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	randomBytes := make([]byte, 32)
	_, err := rand.Read(randomBytes)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Could not generate random file name", err)
		return
	}
	randomBase := base64.RawURLEncoding.EncodeToString(randomBytes)
	filename := randomBase + "." + "mp4"

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

	video, err := cfg.db.CreateVideo(database.CreateVideoParams{
		Title:			"Get title from request",
		Description:	"Get descrition from request",
		UserID:		userID,
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not create video", err)
		return
	}

	const maxMemory = 1 << 30
	r.Body = http.MaxBytesReader(w, r.Body, maxMemory)

	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}
	defer file.Close()

	contentType := header.Header.Get("Content-Type")

	fmt.Println("Received Content-Type:", contentType)

	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "invalid content type", err)
		return
	}

	if mediaType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Unsupported file type", nil)
		return
	}

	tempFile, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not create temp file", err)
		return
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	_, err = io.Copy(tempFile, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not save temp file", err)
		return
	}

	_, err = tempFile.Seek(0, io.SeekStart)
	if err != nil {
    respondWithError(w, http.StatusInternalServerError, "Could not reset file pointer", err)
    return
	}

	aspect, err := cfg.getVideoAspectRatio(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not determine aspect ratio", err)
		return
	}

	prefix := "other"
	switch aspect {
	case "16:9":
		prefix = "landscape"
	case "9:16":
		prefix = "portrait"
	}

	bucket := cfg.s3Bucket
	key := fmt.Sprintf("%s/%s", prefix, filename)

	_, err = cfg.s3Client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:			aws.String(bucket),
		Key:			aws.String(key),
		Body:			tempFile,
		ContentType:	aws.String("video/mp4"),
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not upload to S3", err)
		return
	}

	fmt.Println("Assigned video.VideoURL =", video.VideoURL)

	videoURL := fmt.Sprintf("https://%s/%s", cfg.s3CfDistribution, key)
	video.VideoURL = &videoURL

	fmt.Println("Updating video", video.ID, "with video URL", videoURL)
	err = cfg.db.UpdateVideo(video)
	if err != nil {
		fmt.Println("UpdateVideo error:", err)
		respondWithError(w, http.StatusInternalServerError, "Could not update video metadata", err)
		return
	}

	respondWithJSON(w, http.StatusOK, videoURL)

	fmt.Println("uploading video", video.ID, "by user", userID)
}

func (cfg *apiConfig) getVideoAspectRatio(filePath string) (string, error) {
	type ffprobeOutput struct {
	Streams []struct {
		Width int `json:"width"`
		Height int `json:"height"`
		} `json:"streams"`
	}

	cmd := exec.Command(
	"ffprobe",
	"-v", "error",
	"-print_format", "json",
	"-show_streams",
	filePath,
	)

	buf := new(bytes.Buffer)
	cmd.Stdout = buf
	err := cmd.Run()
	if err != nil {
		return "", err
	}

	var out ffprobeOutput
	err = json.Unmarshal(buf.Bytes(), &out)
	if err != nil {
		return "", err
	}

	if len(out.Streams) == 0 {
		return "", err
	}
	w := out.Streams[0].Width
	h := out.Streams[0].Height

	if w*9 >= h*16-15 && w*9 <= h*16+15 {
		return "16:9", nil
	} else if w*16 >= h*9-15 && w*16 <= h*9+15 {
		return "9:16", nil
	} else {
		return "other", nil
	}
}
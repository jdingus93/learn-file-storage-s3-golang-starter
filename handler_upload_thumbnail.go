package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
	fmt.Printf("handlerUploadThumbnail: path is: %q\n", r.URL.Path)
	parts := strings.Split(r.URL.Path, "/")
	fmt.Printf("handlerUploadThumbnail: path parts: %#v\n", parts)
	if len(parts) != 5 || parts[1] != "api" || parts[2] != "videos" || parts[4] != "thumbnail" {
		fmt.Println("handlerUploadThumbnail: invalid path parts:", parts)
		http.NotFound(w, r)
		return
	}
	videoIDString := parts[3]
	fmt.Println("Extracted videoIDString:", videoIDString)

	var contentTypeToExt = map[string]string{
		"image/png": "png",
		"image/jpeg": "jpg",
		"image/jpg": "jpg",
		"image/gif": "gif",
	}
	fmt.Println(">>> handlerUploadThumbnail called <<<")
	randomBytes := make([]byte, 32)
	_, err := rand.Read(randomBytes)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Could not generate random file name", err)
		return
	}

	fmt.Println("videoIDString is:", videoIDString)
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid video ID", err)
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

	const maxMemory = 1 << 30
	err = r.ParseMultipartForm(maxMemory)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse mulitpart form", err)
		return
	}

	file, header, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}
	defer file.Close()

	contentType := header.Header.Get("Content-Type")

	ext, ok := contentTypeToExt[contentType]
	if !ok {
		respondWithError(w, http.StatusBadRequest, "Unsupported file type", nil)
		return
	}

	randomBase := base64.RawURLEncoding.EncodeToString(randomBytes)
	filename := randomBase + "." + ext
	destPath := filepath.Join(cfg.assetsRoot, filename)

	out, err := os.Create(destPath)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to create file", err)
		return
	}
	defer out.Close()

	_, err = io.Copy(out, file)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to copy file", err)
		return
	}

	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Video not found", err)
		return
	}
	if video.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "You do not own this video", nil)
		return
	}

	thumbnailURL := "http://localhost:8091/assets/" + filename
	video.ThumbnailURL = &thumbnailURL

	fmt.Println("Updating video", video.ID, "with thumbnail URL", thumbnailURL)
	err = cfg.db.UpdateVideo(video)
	if err != nil {
		fmt.Println("UpdateVideo error:", err)
		respondWithError(w, http.StatusInternalServerError, "Could not update video metadata", err)
		return
	}

	respondWithJSON(w, http.StatusOK, video)

	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)
}

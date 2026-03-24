package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
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

	const maxMemory = 10 << 20

	err = r.ParseMultipartForm(maxMemory)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't parse multipartform", err)
	}

	fileData, headers, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't read FormFile", err)
	}

	contentType := headers.Header.Get("Content-Type")

	// imageData, err := io.ReadAll(fileData)
	// if err != nil {
	// 	respondWithError(w, http.StatusInternalServerError, "Couldn't read image data", err)
	// }

	fileExtension, err := mime.ExtensionsByType(contentType)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Invalid mime type", err)
	}

	mediatype, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "couldn't parse mime type ", err)
	}

	if mediatype != "image/jpeg" && mediatype != "image/png" {
		respondWithError(w, http.StatusInternalServerError, "upload must be image/jpeg or image/png", err)
	}

	randomBytes := make([]byte, 32)
	rand.Read(randomBytes)

	randomURLString := base64.RawURLEncoding.EncodeToString(randomBytes)

	filePath := filepath.Join(cfg.assetsRoot, randomURLString+fileExtension[0])
	file, err := os.Create(filePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to create file in file system", err)
	}

	_, err = io.Copy(file, fileData)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to copy data to file", err)
	}

	imageDataURL := fmt.Sprintf("http://localhost:%s/assets/%s%s", cfg.port, randomURLString, fileExtension[0])

	videoMetadata, err := cfg.db.GetVideo(videoID)

	if videoMetadata.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "User is not the owner of the video", err)
	}

	videoMetadata.ThumbnailURL = &imageDataURL

	if err = cfg.db.UpdateVideo(videoMetadata); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to update video", err)
	}

	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	respondWithJSON(w, http.StatusOK, videoMetadata)
}

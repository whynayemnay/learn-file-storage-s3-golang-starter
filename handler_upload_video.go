package main

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<30)
	videoID := r.PathValue("videoID")
	videoUUID, err := uuid.Parse(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't parse videoID to UUID", err)
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

	video, err := cfg.db.GetVideo(videoUUID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "could not fetch video from DB", err)
		return
	}

	if video.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "not video owner", nil)
		return
	}

	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "couldnt parse vide file from form", err)
		return
	}
	defer file.Close()
	mediaType, _, err := mime.ParseMediaType(header.Header.Get("Content-type"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "invalid content type", err)
		return
	}

	if mediaType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "invalid file type", nil)
		return
	}

	tempVideo, err := os.CreateTemp("", "tubley-video.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "error creating file on server", err)
		return
	}
	defer os.Remove(tempVideo.Name())
	defer tempVideo.Close()

	if _, err := io.Copy(tempVideo, file); err != nil {
		respondWithError(w, http.StatusInternalServerError, "error copying file on server", err)
		return
	}

	processedFilePath, err := processVideoForFastStart(tempVideo.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "error processing video", err)
		return
	}
	defer os.Remove(processedFilePath)
	processedFile, err := os.Open(processedFilePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "error opening processed video", err)
		return
	}

	aspect, err := getVideoAspectRation(processedFilePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "error getting aspect ration from file", err)
		return
	}
	var prefix string
	switch aspect {
	case "16:9":
		prefix = "landscape"
	case "9:16":
		prefix = "portrait"
	default:
		prefix = "other"
	}

	key := prefix + "/" + getAssetPath(mediaType)

	_, err = cfg.s3Client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:      aws.String(cfg.s3Bucket),
		Key:         aws.String(key),
		Body:        processedFile,
		ContentType: aws.String(mediaType),
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "error uploading to s3", err)
		return
	}

	vid_url := fmt.Sprintf("https://%s/%s", cfg.s3CfDistribution, key)
	// url := fmt.Sprintf("%s,%s", cfg.s3Bucket, key)
	video.VideoURL = &vid_url
	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "couldnt update video on db", err)
		return
	}

	respondWithJSON(w, http.StatusOK, video)
}

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

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	const maxMemory = 1 << 30
	r.Body = http.MaxBytesReader(w, r.Body, maxMemory)

	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't find JWT", err)
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	videoMetadata, err := cfg.db.GetVideo(videoID)

	if videoMetadata.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "User is not the owner of the video", err)
	}

	fileData, headers, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Coudn't read FormFile", err)
	}
	defer fileData.Close()

	contentType := headers.Header.Get("Content-Type")

	fileExtension, err := mime.ExtensionsByType(contentType)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Invalid mime type", err)
	}

	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "couldn't parse mime type", err)
	}

	if mediaType != "video/mp4" {
		respondWithError(w, http.StatusInternalServerError, "upload must be video/mp4", err)
	}

	tempFile, err := os.CreateTemp("", "tubely-upload.mp4")
	defer os.Remove("tubely-upload.mp4")
	defer tempFile.Close()
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed to allocate local space for video uplaod", err)
	}

	_, err = io.Copy(tempFile, fileData)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed to copy data to file", err)
	}

	aspectRatio, err := getVideoAspectRatio(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to probe the aspect ratio of the video", err)
	}

	var aspectRatioDescriptor string
	switch aspectRatio {
	case "16:9":
		aspectRatioDescriptor = "landscape"
	case "9:16":
		aspectRatioDescriptor = "portrait"
	default:
		aspectRatioDescriptor = "other"
	}

	tempProcessedFilePath, err := processVideoForFastStart(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed to process video", err)
	}

	tempProcessedFile, err := os.Open(tempProcessedFilePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed to retrieve processed video", err)
	}
	defer tempProcessedFile.Close()

	randomBytes := make([]byte, 32)
	rand.Read(randomBytes)
	randomURLString := base64.RawURLEncoding.EncodeToString(randomBytes)
	videoDataKey := fmt.Sprintf("%s/%s%s", aspectRatioDescriptor, randomURLString, fileExtension[3])

	if _, err = tempProcessedFile.Seek(0, io.SeekStart); err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed to seek to start of video", err)
	}

	if _, err = cfg.s3Client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &videoDataKey,
		Body:        tempProcessedFile,
		ContentType: &contentType,
	}); err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed to put object", err)
	}

	// videoURL := fmt.Sprintf("%s,%s", cfg.s3Bucket, videoDataKey)
	videoURL := fmt.Sprintf("https://%s/%s", cfg.s3CfDistribution, videoDataKey)

	videoMetadata.VideoURL = &videoURL

	if err = cfg.db.UpdateVideo(videoMetadata); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to update video record", err)
	}

	fmt.Println("uploading video", videoID, "by user", userID)
	respondWithJSON(w, http.StatusOK, videoMetadata)
}

type ffprobeParams struct {
	Streams []struct {
		AvgFrameRate       string  `json:"avg_frame_rate,omitempty"`
		BitRate            string  `json:"bit_rate,omitempty"`
		BitsPerRawSample   string  `json:"bits_per_raw_sample,omitempty"`
		ChromaLocation     string  `json:"chroma_location,omitempty"`
		CodecLongName      string  `json:"codec_long_name,omitempty"`
		CodecName          string  `json:"codec_name,omitempty"`
		CodecTag           string  `json:"codec_tag,omitempty"`
		CodecTagString     string  `json:"codec_tag_string,omitempty"`
		CodecType          string  `json:"codec_type,omitempty"`
		CodedHeight        float64 `json:"coded_height,omitempty"`
		CodedWidth         float64 `json:"coded_width,omitempty"`
		ColorPrimaries     string  `json:"color_primaries,omitempty"`
		ColorRange         string  `json:"color_range,omitempty"`
		ColorSpace         string  `json:"color_space,omitempty"`
		ColorTransfer      string  `json:"color_transfer,omitempty"`
		DisplayAspectRatio string  `json:"display_aspect_ratio,omitempty"`
		Disposition        struct {
			AttachedPic     float64 `json:"attached_pic,omitempty"`
			Captions        float64 `json:"captions,omitempty"`
			CleanEffects    float64 `json:"clean_effects,omitempty"`
			Comment         float64 `json:"comment,omitempty"`
			Default         float64 `json:"default,omitempty"`
			Dependent       float64 `json:"dependent,omitempty"`
			Descriptions    float64 `json:"descriptions,omitempty"`
			Dub             float64 `json:"dub,omitempty"`
			Forced          float64 `json:"forced,omitempty"`
			HearingImpaired float64 `json:"hearing_impaired,omitempty"`
			Karaoke         float64 `json:"karaoke,omitempty"`
			Lyrics          float64 `json:"lyrics,omitempty"`
			Metadata        float64 `json:"metadata,omitempty"`
			Multilayer      float64 `json:"multilayer,omitempty"`
			NonDiegetic     float64 `json:"non_diegetic,omitempty"`
			Original        float64 `json:"original,omitempty"`
			StillImage      float64 `json:"still_image,omitempty"`
			TimedThumbnails float64 `json:"timed_thumbnails,omitempty"`
			VisualImpaired  float64 `json:"visual_impaired,omitempty"`
		} `json:"disposition,omitempty"`
		Duration          string  `json:"duration,omitempty"`
		DurationTs        float64 `json:"duration_ts,omitempty"`
		ExtradataSize     float64 `json:"extradata_size,omitempty"`
		FieldOrder        string  `json:"field_order,omitempty"`
		HasBFrames        float64 `json:"has_b_frames,omitempty"`
		Height            float64 `json:"height,omitempty"`
		ID                string  `json:"id,omitempty"`
		Index             float64 `json:"index,omitempty"`
		IsAvc             string  `json:"is_avc,omitempty"`
		Level             float64 `json:"level,omitempty"`
		NalLengthSize     string  `json:"nal_length_size,omitempty"`
		NbFrames          string  `json:"nb_frames,omitempty"`
		PixFmt            string  `json:"pix_fmt,omitempty"`
		Profile           string  `json:"profile,omitempty"`
		RFrameRate        string  `json:"r_frame_rate,omitempty"`
		Refs              float64 `json:"refs,omitempty"`
		SampleAspectRatio string  `json:"sample_aspect_ratio,omitempty"`
		StartPts          float64 `json:"start_pts,omitempty"`
		StartTime         string  `json:"start_time,omitempty"`
		Tags              struct {
			Encoder     string `json:"encoder,omitempty"`
			HandlerName string `json:"handler_name,omitempty"`
			Language    string `json:"language,omitempty"`
			Timecode    string `json:"timecode,omitempty"`
			VendorID    string `json:"vendor_id,omitempty"`
		} `json:"tags,omitempty"`
		TimeBase string  `json:"time_base,omitempty"`
		Width    float64 `json:"width,omitempty"`
	} `json:"streams,omitempty"`
}

func getVideoAspectRatio(filePath string) (string, error) {
	cmd := exec.Command(
		"ffprobe",
		"-v",
		"error",
		"-print_format",
		"json",
		"-show_streams",
		filePath,
	)

	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		return "", err
	}

	var params ffprobeParams
	if err := json.Unmarshal(stdout.Bytes(), &params); err != nil {
		return "", err
	}

	aspectRatio := params.Streams[0].DisplayAspectRatio
	return aspectRatio, nil
}

func processVideoForFastStart(filePath string) (string, error) {
	outputFilePath := filePath + ".processing"

	cmd := exec.Command(
		"ffmpeg",
		"-i",
		filePath,
		"-c",
		"copy",
		"-movflags",
		"faststart",
		"-f",
		"mp4",
		outputFilePath,
	)

	if err := cmd.Run(); err != nil {
		return "", err
	}

	return outputFilePath, nil
}

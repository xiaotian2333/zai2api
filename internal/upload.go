package internal

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrRequestFailed 统一的请求失败错误
var ErrRequestFailed = errors.New("请求失败")

// MediaType 媒体类型
type MediaType string

const (
	MediaTypeImage MediaType = "image"
	MediaTypeVideo MediaType = "video"
)

// FileUploadResponse z.ai 文件上传响应
type FileUploadResponse struct {
	ID        string                 `json:"id"`
	UserID    string                 `json:"user_id"`
	Hash      *string                `json:"hash"`
	Filename  string                 `json:"filename"`
	Data      map[string]interface{} `json:"data"`
	Meta      FileMeta               `json:"meta"`
	CreatedAt int64                  `json:"created_at"`
	UpdatedAt int64                  `json:"updated_at"`
}

// FileMeta 文件元数据
type FileMeta struct {
	Name        string                 `json:"name"`
	ContentType string                 `json:"content_type"`
	Size        int64                  `json:"size"`
	Data        map[string]interface{} `json:"data"`
	OssEndpoint string                 `json:"oss_endpoint"`
	CdnURL      string                 `json:"cdn_url"`
}

// UpstreamFile 上游请求的文件格式
type UpstreamFile struct {
	Type   string             `json:"type"`
	File   FileUploadResponse `json:"file"`
	ID     string             `json:"id"`
	URL    string             `json:"url"`
	Name   string             `json:"name"`
	Status string             `json:"status"`
	Size   int64              `json:"size"`
	Error  string             `json:"error"`
	ItemID string             `json:"itemId"`
	Media  string             `json:"media"`
}

// mimeExtMap MIME 类型到扩展名映射
var mimeExtMap = map[string]string{
	// 图片
	"image/png":     ".png",
	"image/jpeg":    ".jpg",
	"image/jpg":     ".jpg",
	"image/gif":     ".gif",
	"image/webp":    ".webp",
	"image/bmp":     ".bmp",
	"image/svg+xml": ".svg",
	// 视频
	"video/mp4":        ".mp4",
	"video/webm":       ".webm",
	"video/quicktime":  ".mov",
	"video/x-msvideo":  ".avi",
	"video/mpeg":       ".mpeg",
	"video/x-matroska": ".mkv",
}

// detectMediaType 根据 MIME 类型判断媒体类型
func detectMediaType(contentType string) MediaType {
	if strings.HasPrefix(contentType, "video/") {
		return MediaTypeVideo
	}
	return MediaTypeImage
}

// getExtFromMime 根据 MIME 类型获取文件扩展名
func getExtFromMime(contentType string, mediaType MediaType) string {
	// 精确匹配
	if ext, ok := mimeExtMap[contentType]; ok {
		return ext
	}
	// 模糊匹配
	for mime, ext := range mimeExtMap {
		if strings.Contains(contentType, strings.TrimPrefix(mime, "image/")) ||
			strings.Contains(contentType, strings.TrimPrefix(mime, "video/")) {
			return ext
		}
	}
	// 默认
	if mediaType == MediaTypeVideo {
		return ".mp4"
	}
	return ".png"
}

// parseBase64Data 解析 base64 数据 URL
func parseBase64Data(dataURL string) (data []byte, contentType string, err error) {
	// 格式: data:image/jpeg;base64,/9j/4AAQ... 或 data:video/mp4;base64,...
	parts := strings.SplitN(dataURL, ",", 2)
	if len(parts) != 2 {
		return nil, "", ErrRequestFailed
	}

	// 解析 MIME 类型
	header := parts[0]
	if idx := strings.Index(header, ":"); idx != -1 {
		mimeAndEncoding := header[idx+1:]
		if semiIdx := strings.Index(mimeAndEncoding, ";"); semiIdx != -1 {
			contentType = mimeAndEncoding[:semiIdx]
		}
	}

	// 解码 base64
	data, err = base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		LogError("base64 decode error: %v", err)
		return nil, "", ErrRequestFailed
	}
	return data, contentType, nil
}

// downloadFromURL 从 URL 下载文件
func downloadFromURL(url string) (data []byte, contentType string, filename string, err error) {
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		LogError("download error: %v", err)
		return nil, "", "", ErrRequestFailed
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		LogError("download failed: status %d", resp.StatusCode)
		return nil, "", "", ErrRequestFailed
	}

	data, err = io.ReadAll(resp.Body)
	if err != nil {
		LogError("read response error: %v", err)
		return nil, "", "", ErrRequestFailed
	}

	contentType = resp.Header.Get("Content-Type")
	filename = filepath.Base(url)
	// 去掉 URL 参数
	if idx := strings.Index(filename, "?"); idx != -1 {
		filename = filename[:idx]
	}
	return data, contentType, filename, nil
}

// uploadToZAI 上传文件到 z.ai
func uploadToZAI(token string, data []byte, filename string, contentType string) (*FileUploadResponse, error) {
	LogDebug("[UploadToZAI] Preparing request: filename=%s, contentType=%s, dataSize=%d", filename, contentType, len(data))
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		LogError("create form file error: %v", err)
		return nil, ErrRequestFailed
	}

	if _, err := part.Write(data); err != nil {
		LogError("write file data error: %v", err)
		return nil, ErrRequestFailed
	}
	writer.Close()

	req, err := http.NewRequest("POST", "https://chat.z.ai/api/v1/files/", &buf)
	if err != nil {
		LogError("create request error: %v", err)
		return nil, ErrRequestFailed
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Origin", "https://chat.z.ai")
	req.Header.Set("Referer", "https://chat.z.ai/")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		LogError("upload request error: %v", err)
		return nil, ErrRequestFailed
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		LogError("upload failed: status %d, body: %s", resp.StatusCode, string(body))
		return nil, ErrRequestFailed
	}

	respBody, _ := io.ReadAll(resp.Body)
	LogDebug("[UploadToZAI] Response body: %s", string(respBody))

	var uploadResp FileUploadResponse
	if err := json.Unmarshal(respBody, &uploadResp); err != nil {
		LogError("parse upload response error: %v", err)
		return nil, ErrRequestFailed
	}
	LogDebug("[UploadToZAI] Parsed response: id=%s, filename=%s, size=%d", uploadResp.ID, uploadResp.Filename, uploadResp.Meta.Size)
	return &uploadResp, nil
}

// UploadMedia 通用媒体上传（支持图片和视频，支持 base64 和 URL）
func UploadMedia(token string, mediaURL string, mediaType MediaType) (*UpstreamFile, error) {
	var fileData []byte
	var filename string
	var contentType string

	// 记录上传开始
	urlPreview := mediaURL
	if len(urlPreview) > 100 {
		urlPreview = urlPreview[:100] + "..."
	}
	LogDebug("[Upload] Starting upload: type=%s, url=%s", mediaType, urlPreview)

	if strings.HasPrefix(mediaURL, "data:") {
		// Base64 编码
		var err error
		fileData, contentType, err = parseBase64Data(mediaURL)
		if err != nil {
			LogDebug("[Upload] Base64 parse failed: %v", err)
			return nil, err
		}
		LogDebug("[Upload] Base64 parsed: contentType=%s, dataSize=%d bytes", contentType, len(fileData))
		// 根据 MIME 类型确定默认
		if contentType == "" {
			if mediaType == MediaTypeVideo {
				contentType = "video/mp4"
			} else {
				contentType = "image/png"
			}
		}
		ext := getExtFromMime(contentType, mediaType)
		filename = uuid.New().String()[:12] + ext
	} else {
		// 从 URL 下载
		var err error
		fileData, contentType, filename, err = downloadFromURL(mediaURL)
		if err != nil {
			LogDebug("[Upload] URL download failed: %v", err)
			return nil, err
		}
		LogDebug("[Upload] Downloaded from URL: filename=%s, contentType=%s, size=%d bytes", filename, contentType, len(fileData))
		// 检查文件名有效性
		if filename == "" || filename == "." || filename == "/" {
			if contentType == "" {
				if mediaType == MediaTypeVideo {
					contentType = "video/mp4"
				} else {
					contentType = "image/png"
				}
			}
			ext := getExtFromMime(contentType, mediaType)
			filename = uuid.New().String()[:12] + ext
		}
	}

	// 自动检测媒体类型
	if contentType != "" {
		detectedType := detectMediaType(contentType)
		if detectedType != mediaType {
			mediaType = detectedType
		}
	}

	// 上传到 z.ai
	LogDebug("[Upload] Uploading to z.ai: filename=%s, contentType=%s, size=%d bytes", filename, contentType, len(fileData))
	uploadResp, err := uploadToZAI(token, fileData, filename, contentType)
	if err != nil {
		LogDebug("[Upload] Upload to z.ai failed: %v", err)
		return nil, err
	}
	LogDebug("[Upload] Upload success: id=%s, cdnURL=%s", uploadResp.ID, uploadResp.Meta.CdnURL)

	return &UpstreamFile{
		Type:   string(mediaType),
		File:   *uploadResp,
		ID:     uploadResp.ID,
		URL:    "/api/v1/files/" + uploadResp.ID + "/content",
		Name:   uploadResp.Filename,
		Status: "uploaded",
		Size:   uploadResp.Meta.Size,
		Error:  "",
		ItemID: uuid.New().String(),
		Media:  string(mediaType),
	}, nil
}

// UploadImageFromURL 从 URL 或 base64 上传图片到 z.ai
func UploadImageFromURL(token string, imageURL string) (*UpstreamFile, error) {
	return UploadMedia(token, imageURL, MediaTypeImage)
}

// UploadVideoFromURL 从 URL 或 base64 上传视频到 z.ai
func UploadVideoFromURL(token string, videoURL string) (*UpstreamFile, error) {
	return UploadMedia(token, videoURL, MediaTypeVideo)
}

// UploadImages 批量上传图片
func UploadImages(token string, imageURLs []string) ([]*UpstreamFile, error) {
	LogDebug("[UploadImages] Starting batch upload: count=%d", len(imageURLs))
	var files []*UpstreamFile
	for i, url := range imageURLs {
		LogDebug("[UploadImages] Uploading image %d/%d", i+1, len(imageURLs))
		file, err := UploadImageFromURL(token, url)
		if err != nil {
			LogError("upload image failed: %s - %v", url[:min(50, len(url))], err)
			continue
		}
		LogDebug("[UploadImages] Image %d uploaded: id=%s", i+1, file.ID)
		files = append(files, file)
	}
	LogDebug("[UploadImages] Batch upload complete: success=%d/%d", len(files), len(imageURLs))
	return files, nil
}

// UploadVideos 批量上传视频
func UploadVideos(token string, videoURLs []string) ([]*UpstreamFile, error) {
	LogDebug("[UploadVideos] Starting batch upload: count=%d", len(videoURLs))
	var files []*UpstreamFile
	for i, url := range videoURLs {
		LogDebug("[UploadVideos] Uploading video %d/%d", i+1, len(videoURLs))
		file, err := UploadVideoFromURL(token, url)
		if err != nil {
			LogError("upload video failed: %s - %v", url[:min(50, len(url))], err)
			continue
		}
		LogDebug("[UploadVideos] Video %d uploaded: id=%s", i+1, file.ID)
		files = append(files, file)
	}
	LogDebug("[UploadVideos] Batch upload complete: success=%d/%d", len(files), len(videoURLs))
	return files, nil
}

// UploadMediaFiles 批量上传媒体文件（图片+视频）
func UploadMediaFiles(token string, imageURLs, videoURLs []string) ([]*UpstreamFile, []*UpstreamFile, error) {
	images, _ := UploadImages(token, imageURLs)
	videos, _ := UploadVideos(token, videoURLs)
	return images, videos, nil
}

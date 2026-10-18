// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package imagehosting

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/textproto"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/net/http/httpguts"
	"golang.org/x/net/publicsuffix"

	"github.com/autobrr/upbrr/internal/httpclient"
	"github.com/autobrr/upbrr/pkg/api"
)

// These are local resource limits, not claims about the service's upload limits.
const (
	slowpicsOrigin                   = "https://slow.pics"
	slowpicsMaxImageBytes      int64 = 20 << 20
	slowpicsMaxCollectionBytes int64 = 100 << 20
	slowpicsMaxImagePixels           = 64 << 20
	slowpicsMaxResponseBytes   int64 = 1 << 20
	slowpicsMaxMemberAttempts        = 3
	slowpicsMaxRetryDelay            = 30 * time.Second
)

// errSlowpicsOutcomeUnknown prevents creating another collection after a create
// attempt, including when the server's response or a later image upload fails.
var errSlowpicsOutcomeUnknown = errors.New("slow.pics collection may already exist; reconcile before retrying")

type slowpicsCollectionImage struct {
	Name string
	Path string
}

type slowpicsCollectionRequest struct {
	Name   string
	Images []slowpicsCollectionImage
}

// slowpicsCollectionURL is a completed collection page, never a direct image URL.
type slowpicsCollectionURL string

type slowpicsUploader struct {
	client *http.Client
}

type slowpicsImage struct {
	name string
	data []byte
}

type slowpicsCreatedCollection struct {
	CollectionUUID string     `json:"collectionUuid"`
	Key            string     `json:"key"`
	Images         [][]string `json:"images"`
}

type slowpicsResponse struct {
	status      int
	body        []byte
	contentType string
	retryAfter  string
}

// UploadCollection validates and snapshots all PNGs before creating an anonymous
// flat collection. Only a fully uploaded collection yields a URL. Create requests
// are never retried; any failure from that point retains the unknown-outcome marker.
func (u *slowpicsUploader) UploadCollection(ctx context.Context, input slowpicsCollectionRequest) (slowpicsCollectionURL, error) {
	images, err := validateSlowpicsCollectionRequest(ctx, input)
	if err != nil {
		return "", err
	}
	return u.uploadValidatedCollection(ctx, input, images)
}

// uploadValidatedCollection consumes the same snapshots used by the durable
// coordinator to fingerprint and reserve this upload; it never reopens source files.
func (u *slowpicsUploader) uploadValidatedCollection(
	ctx context.Context,
	input slowpicsCollectionRequest,
	images []slowpicsImage,
) (slowpicsCollectionURL, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if err != nil {
		return "", errors.New("slow.pics anonymous session unavailable")
	}
	client := &http.Client{
		Timeout:       httpclient.UploadTimeout,
		Jar:           jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	if u.client != nil {
		client.Transport = u.client.Transport
	}
	browserID, err := uuid.NewRandom()
	if err != nil {
		return "", errors.New("slow.pics browser identifier unavailable")
	}
	response, err := slowpicsRequest(ctx, client, http.MethodGet, "/comparison", nil, "")
	if err != nil {
		return "", err
	}
	if response.status != http.StatusOK {
		return "", fmt.Errorf("slow.pics anonymous session unavailable (HTTP %d)", response.status)
	}
	if _, err := slowpicsSessionToken(client, "/upload/collection"); err != nil {
		return "", err
	}
	fields := map[string]string{
		"collectionName": input.Name,
		"hentai":         "false",
		"optimizeImages": "false",
		"public":         "false",
		"browserId":      browserID.String(),
	}
	for index, image := range images {
		fields[fmt.Sprintf("imageNames[%d]", index)] = image.name
	}
	body, contentType, err := slowpicsMultipart(fields, nil)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("slow.pics collection canceled: %w", err)
	}
	response, err = slowpicsRequest(ctx, client, http.MethodPost, "/upload/collection", body, contentType)
	if err != nil {
		return "", errors.Join(errSlowpicsOutcomeUnknown, err)
	}
	created, err := parseSlowpicsCollection(response, len(images))
	if err != nil {
		return "", errors.Join(errSlowpicsOutcomeUnknown, err)
	}
	for index, image := range images {
		if err := uploadSlowpicsImage(ctx, client, created.CollectionUUID, created.Images[0][index], browserID.String(), image); err != nil {
			return "", errors.Join(errSlowpicsOutcomeUnknown, fmt.Errorf("slow.pics image %d: %w", index+1, err))
		}
	}
	return slowpicsCollectionURL(slowpicsOrigin + "/c/" + created.Key), nil
}

// validateSlowpicsCollectionRequest keeps validated bytes in memory so files
// cannot change between preflight and upload. Limits bound encoded and decoded data.
func validateSlowpicsCollectionRequest(ctx context.Context, input slowpicsCollectionRequest) ([]slowpicsImage, error) {
	if !validSlowpicsName(input.Name) || len(input.Images) == 0 || len(input.Images) > 100 {
		return nil, errors.New("slow.pics requires a collection name and between 1 and 100 PNG images")
	}
	images := make([]slowpicsImage, 0, len(input.Images))
	var total int64
	for index, image := range input.Images {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("slow.pics validation canceled: %w", err)
		}
		if !validSlowpicsName(image.Name) {
			return nil, fmt.Errorf("slow.pics image %d requires a valid display name", index+1)
		}
		data, err := readSlowpicsPNG(image.Path, min(slowpicsMaxImageBytes, slowpicsMaxCollectionBytes-total))
		if err != nil {
			return nil, fmt.Errorf("slow.pics image %d: %w", index+1, err)
		}
		total += int64(len(data))
		images = append(images, slowpicsImage{name: image.Name, data: data})
	}
	return images, nil
}

func validSlowpicsName(value string) bool {
	return value != "" && value == api.CollectionLabel(value, "")
}

func readSlowpicsPNG(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot read local PNG")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > limit {
		return nil, errors.New("local PNG is not a regular file within the upload size limit")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("cannot read local PNG within the upload size limit")
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > slowpicsMaxImagePixels {
		return nil, errors.New("local image is not a valid PNG within the pixel limit")
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		return nil, errors.New("local PNG is incomplete or corrupt")
	}
	return data, nil
}

func parseSlowpicsCollection(response slowpicsResponse, count int) (slowpicsCreatedCollection, error) {
	var created slowpicsCreatedCollection
	if response.status < http.StatusOK || response.status >= http.StatusMultipleChoices {
		return created, fmt.Errorf("slow.pics collection creation unavailable (HTTP %d)", response.status)
	}
	contentType, _, err := mime.ParseMediaType(response.contentType)
	if err != nil || contentType != "application/json" || json.Unmarshal(response.body, &created) != nil {
		return created, errors.New("slow.pics collection response is not valid JSON")
	}
	if !validSlowpicsUUID(created.CollectionUUID) || !validSlowpicsKey(created.Key) || len(created.Images) != 1 || len(created.Images[0]) != count {
		return created, errors.New("slow.pics collection response is incomplete or invalid")
	}
	seen := make(map[string]struct{}, count)
	for _, id := range created.Images[0] {
		normalized := strings.ToLower(id)
		if !validSlowpicsUUID(id) {
			return created, errors.New("slow.pics collection response contains an invalid image identifier")
		}
		if _, exists := seen[normalized]; exists {
			return created, errors.New("slow.pics collection response contains duplicate image identifiers")
		}
		seen[normalized] = struct{}{}
	}
	return created, nil
}

func validSlowpicsUUID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed != uuid.Nil && parsed.String() == strings.ToLower(value)
}

func validSlowpicsKey(key string) bool {
	if len(key) == 0 || len(key) > 128 {
		return false
	}
	for _, char := range key {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' {
			continue
		}
		return false
	}
	return true
}

func uploadSlowpicsImage(ctx context.Context, client *http.Client, collectionID, imageID, browserID string, image slowpicsImage) error {
	body, contentType, err := slowpicsMultipart(map[string]string{
		"collectionUuid": collectionID,
		"imageUuid":      imageID,
		"browserId":      browserID,
	}, image.data)
	if err != nil {
		return err
	}
	for attempt := range slowpicsMaxMemberAttempts {
		response, err := slowpicsRequest(ctx, client, http.MethodPost, "/upload/image/"+imageID, body, contentType)
		if err != nil {
			return err
		}
		if response.status >= http.StatusOK && response.status < http.StatusMultipleChoices &&
			!strings.Contains(strings.ToLower(response.contentType), "html") &&
			!bytes.HasPrefix(bytes.TrimSpace(response.body), []byte("<")) {
			return nil
		}
		if attempt == slowpicsMaxMemberAttempts-1 ||
			response.status != http.StatusTooManyRequests && (response.status < http.StatusInternalServerError || response.status >= 600) {
			return fmt.Errorf("image upload was not acknowledged (HTTP %d)", response.status)
		}
		delay, retry := slowpicsRetryDelay(response.retryAfter, time.Now(), attempt)
		if !retry {
			return errors.New("image upload retry delay exceeds the upload limit")
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("image upload canceled: %w", ctx.Err())
		case <-timer.C:
		}
	}
	return errors.New("image upload was not acknowledged")
}

func slowpicsRetryDelay(value string, now time.Time, attempt int) (time.Duration, bool) {
	delay := time.Duration(attempt+1) * time.Second
	seconds, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	if errors.Is(err, strconv.ErrRange) {
		return 0, false
	}
	if err == nil {
		if seconds > uint64(slowpicsMaxRetryDelay/time.Second) {
			return 0, false
		}
		delay = time.Duration(seconds) * time.Second
	} else if when, err := http.ParseTime(value); err == nil {
		delay = max(0, when.Sub(now))
	}
	return delay, delay <= slowpicsMaxRetryDelay
}

func slowpicsMultipart(fields map[string]string, image []byte) ([]byte, string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, value := range fields {
		if err := writer.WriteField(name, value); err != nil {
			return nil, "", errors.New("slow.pics cannot prepare upload fields")
		}
	}
	if image != nil {
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", `form-data; name="file"; filename="image.png"`)
		header.Set("Content-Type", "image/png")
		part, err := writer.CreatePart(header)
		if err != nil {
			return nil, "", errors.New("slow.pics cannot prepare PNG upload")
		}
		if _, err := part.Write(image); err != nil {
			return nil, "", errors.New("slow.pics cannot prepare PNG upload")
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", errors.New("slow.pics cannot finish upload fields")
	}
	return body.Bytes(), writer.FormDataContentType(), nil
}

func slowpicsRequest(ctx context.Context, client *http.Client, method, endpoint string, body []byte, contentType string) (slowpicsResponse, error) {
	var result slowpicsResponse
	request, err := http.NewRequestWithContext(ctx, method, slowpicsOrigin+endpoint, bytes.NewReader(body))
	if err != nil {
		return result, errors.New("slow.pics cannot prepare request")
	}
	request.Header.Set("User-Agent", "upbrr")
	request.Header.Set("Origin", slowpicsOrigin)
	request.Header.Set("Referer", slowpicsOrigin+"/comparison")
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if method == http.MethodPost {
		token, err := slowpicsSessionToken(client, endpoint)
		if err != nil {
			return result, err
		}
		request.Header.Set("X-Xsrf-Token", token)
	}
	response, err := client.Do(request)
	if err != nil {
		if err := ctx.Err(); err != nil {
			return result, fmt.Errorf("slow.pics request canceled: %w", err)
		}
		return result, errors.New("slow.pics request failed")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, slowpicsMaxResponseBytes+1))
	if err != nil || int64(len(data)) > slowpicsMaxResponseBytes {
		return result, errors.New("slow.pics response unavailable or exceeds the size limit")
	}
	return slowpicsResponse{
		status:      response.StatusCode,
		body:        data,
		contentType: response.Header.Get("Content-Type"),
		retryAfter:  response.Header.Get("Retry-After"),
	}, nil
}

// slowpicsSessionToken checks cookies applicable to the actual upload endpoint,
// rather than accepting a token scoped only to the comparison page.
func slowpicsSessionToken(client *http.Client, endpoint string) (string, error) {
	target, err := url.Parse(slowpicsOrigin + endpoint)
	if err != nil || client.Jar == nil {
		return "", errors.New("slow.pics anonymous session token is unavailable")
	}
	for _, cookie := range client.Jar.Cookies(target) {
		if cookie.Name != "XSRF-TOKEN" {
			continue
		}
		token, err := url.PathUnescape(cookie.Value)
		if err != nil || strings.TrimSpace(token) == "" || !httpguts.ValidHeaderFieldValue(token) {
			return "", errors.New("slow.pics anonymous session token is invalid")
		}
		return token, nil
	}
	return "", errors.New("slow.pics anonymous session token is unavailable")
}

package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
)

// Picture is an image a model made.
type Picture struct {
	MIME string
	Data []byte
}

// Ext is the file extension that goes with the picture's type.
func (p Picture) Ext() string {
	switch p.MIME {
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	}
	return ".png"
}

// maxPicture bounds what is read of a picture, so a service that sends something else cannot fill the memory.
const maxPicture = 64 << 20

// ImageOptions steer a picture; zero values leave the choice to the model.
type ImageOptions struct {
	Size  string // "1024x1024"
	Steps int    // how many steps it takes to draw; only a model on this computer takes it
}

// Generate asks an image model for a picture of prompt, the way services with an OpenAI-style interface take it:
// POST "/images/generations" asking for the picture inline, or by an address that is then fetched.
func (e Endpoint) Generate(ctx context.Context, prompt string, opt ImageOptions) (Picture, error) {
	request := map[string]any{"model": e.Model, "prompt": prompt, "n": 1, "response_format": "b64_json"}
	if opt.Size != "" {
		request["size"] = opt.Size
	}
	if e.Local {
		request["prompt"] = localPrompt(prompt, opt)
	}
	body, err := json.Marshal(request)
	if err != nil {
		return Picture{}, err
	}
	return e.askForPicture(ctx, strings.TrimSuffix(e.URL, "/")+"/images/generations", "application/json", body)
}

// localPrompt is a prompt with what stable-diffusion.cpp takes inside it, as its interface has no field for that.
func localPrompt(prompt string, opt ImageOptions) string {
	if opt.Steps <= 0 {
		return prompt + ` <sd_cpp_extra_args>{"seed":-1}</sd_cpp_extra_args>`
	}
	return fmt.Sprintf("%s <sd_cpp_extra_args>{\"seed\":-1,\"sample_params\":{\"sample_steps\":%d}}</sd_cpp_extra_args>", prompt, opt.Steps)
}

// askForPicture sends a request and reads the picture from its answer: inline, or by an address that is then fetched.
func (e Endpoint) askForPicture(ctx context.Context, address, contentType string, body []byte) (Picture, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, address, bytes.NewReader(body))
	if err != nil {
		return Picture{}, err
	}
	req.Header.Set("Content-Type", contentType)
	if e.Key != "" {
		req.Header.Set("Authorization", "Bearer "+e.Key)
	}
	client := e.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Picture{}, ctx.Err()
		}
		return Picture{}, fmt.Errorf("could not reach the model: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Picture{}, refusal(resp)
	}
	var out struct {
		Data []struct {
			B64 string `json:"b64_json"`
			URL string `json:"url"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2*maxPicture)).Decode(&out); err != nil {
		return Picture{}, fmt.Errorf("the service's answer could not be read: %w", err)
	}
	if len(out.Data) == 0 {
		return Picture{}, errors.New("the service made no picture")
	}
	var data []byte
	switch d := out.Data[0]; {
	case d.B64 != "":
		if data, err = base64.StdEncoding.DecodeString(d.B64); err != nil {
			return Picture{}, fmt.Errorf("the picture could not be decoded: %w", err)
		}
	case d.URL != "":
		if data, err = e.fetch(ctx, client, d.URL); err != nil {
			return Picture{}, err
		}
	default:
		return Picture{}, errors.New("the service made no picture")
	}
	mime := http.DetectContentType(data)
	if !strings.HasPrefix(mime, "image/") {
		return Picture{}, errors.New("what the service made is not a picture")
	}
	return Picture{MIME: mime, Data: data}, nil
}

// Redraw asks an image model for a new version of source, as prompt says. The engine of this computer takes the picture
// as a form at "/images/edits", and a service takes it in JSON.
func (e Endpoint) Redraw(ctx context.Context, prompt string, source Image, opt ImageOptions) (Picture, error) {
	address := strings.TrimSuffix(e.URL, "/") + "/images/edits"
	var body []byte
	var contentType string
	if e.Local {
		var form bytes.Buffer
		w := multipart.NewWriter(&form)
		fields := map[string]string{"prompt": localPrompt(prompt, opt), "response_format": "b64_json", "n": "1", "model": e.Model}
		if opt.Size != "" {
			fields["size"] = opt.Size
		}
		for name, value := range fields {
			if err := w.WriteField(name, value); err != nil {
				return Picture{}, err
			}
		}
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", `form-data; name="image[]"; filename="source"`)
		header.Set("Content-Type", source.MIME)
		part, err := w.CreatePart(header)
		if err != nil {
			return Picture{}, err
		}
		if _, err := part.Write(source.Data); err != nil {
			return Picture{}, err
		}
		if err := w.Close(); err != nil {
			return Picture{}, err
		}
		body, contentType = form.Bytes(), w.FormDataContentType()
	} else {
		var err error
		body, err = json.Marshal(map[string]any{
			"model": e.Model, "prompt": prompt, "n": 1, "response_format": "b64_json",
			"image": map[string]string{"url": dataURL(source), "type": "image_url"},
		})
		if err != nil {
			return Picture{}, err
		}
		contentType = "application/json"
	}
	return e.askForPicture(ctx, address, contentType, body)
}

// fetch downloads a picture the service made available at an address.
func (e Endpoint) fetch(ctx context.Context, client *http.Client, address string) ([]byte, error) {
	if !validWebURL(address) {
		return nil, errors.New("the service gave an address that is not http or https")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("the picture could not be fetched: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the picture could not be fetched: %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxPicture+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxPicture {
		return nil, errors.New("the picture is too large")
	}
	return data, nil
}

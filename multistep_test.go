// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package genai

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"time"
)

var customTestMethods = map[string]func(ctx context.Context, client *Client, item *testTableItem) []reflect.Value{
	"shared/batches/create_delete":          createDelete,
	"shared/batches/create_get_cancel":      createGetCancelBatches,
	"shared/caches/create_get_delete":       createGetDelete,
	"shared/caches/create_update_get":       createUpdateGet,
	"shared/chats/send_message":             sendMessage,
	"shared/chats/send_message_stream":      sendMessageStream,
	"shared/files/upload_get_delete":        uploadGetDelete,
	"shared/models/generate_content_stream": generateContentStream,
	"shared/tunings/create_get_cancel":      createGetCancelTunings,
}

func wrapResults(resp any, err error) []reflect.Value {
	vResp := reflect.ValueOf(resp)
	if !vResp.IsValid() {
		vResp = reflect.Zero(reflect.TypeOf((*GenerateContentResponse)(nil)))
	}
	vErr := reflect.ValueOf(err)
	if !vErr.IsValid() {
		vErr = reflect.Zero(reflect.TypeOf((*error)(nil)).Elem())
	}
	return []reflect.Value{vResp, vErr}
}

func createDelete(ctx context.Context, client *Client, item *testTableItem) []reflect.Value {
	params := struct {
		Model  string                `json:"model"`
		Src    *BatchJobSource       `json:"src"`
		Config *CreateBatchJobConfig `json:"config"`
	}{}
	paramsJSON, _ := json.Marshal(item.Parameters)
	if err := json.Unmarshal(paramsJSON, &params); err != nil {
		return wrapResults(nil, err)
	}

	batchJob, err := client.Batches.Create(ctx, params.Model, params.Src, params.Config)
	if err != nil {
		return wrapResults(nil, err)
	}
	// if pending then don't delete to avoid error
	batchJob, err = client.Batches.Get(ctx, batchJob.Name, nil)
	if err != nil {
		return wrapResults(nil, err)
	}
	if batchJob.State != JobStatePending {
		return wrapResults(client.Batches.Delete(ctx, batchJob.Name, nil))
	}
	return wrapResults(batchJob, nil)
}

func createGetCancelBatches(ctx context.Context, client *Client, item *testTableItem) []reflect.Value {
	params := struct {
		Model  string                `json:"model"`
		Src    *BatchJobSource       `json:"src"`
		Config *CreateBatchJobConfig `json:"config"`
	}{}
	paramsJSON, _ := json.Marshal(item.Parameters)
	if err := json.Unmarshal(paramsJSON, &params); err != nil {
		return wrapResults(nil, err)
	}

	batchJob, err := client.Batches.Create(ctx, params.Model, params.Src, params.Config)
	if err != nil {
		return wrapResults(nil, err)
	}
	batchJob, err = client.Batches.Get(ctx, batchJob.Name, nil)
	if err != nil {
		return wrapResults(nil, err)
	}
	err = client.Batches.Cancel(ctx, batchJob.Name, nil)
	return wrapResults(nil, err)
}

func createGetCancelTunings(ctx context.Context, client *Client, item *testTableItem) []reflect.Value {
	params := struct {
		BaseModel       string                 `json:"baseModel"`
		TrainingDataset *TuningDataset         `json:"trainingDataset"`
		Config          *CreateTuningJobConfig `json:"config"`
	}{}
	paramsJSON, _ := json.Marshal(item.Parameters)
	if err := json.Unmarshal(paramsJSON, &params); err != nil {
		return wrapResults(nil, err)
	}

	tuningJob, err := client.Tunings.Tune(ctx, params.BaseModel, params.TrainingDataset, params.Config)
	if err != nil {
		return wrapResults(nil, err)
	}
	tuningJob, err = client.Tunings.Get(ctx, tuningJob.Name, nil)
	if err != nil {
		return wrapResults(nil, err)
	}
	_, err = client.Tunings.Cancel(ctx, tuningJob.Name, nil)
	return wrapResults(nil, err)
}

func createGetDelete(ctx context.Context, client *Client, item *testTableItem) []reflect.Value {
	params := struct {
		Model  string                     `json:"model"`
		Config *CreateCachedContentConfig `json:"config"`
	}{}
	paramsJSON, _ := json.Marshal(item.Parameters)
	if err := json.Unmarshal(paramsJSON, &params); err != nil {
		return wrapResults(nil, err)
	}

	var cache *CachedContent
	var err error
	if client.clientConfig.Backend == BackendVertexAI {
		cache, err = client.Caches.Create(ctx, params.Model, params.Config)
	} else {
		filePath := "tests/data/google.png"
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			os.MkdirAll(filepath.Dir(filePath), 0755)            // nolint:errcheck
			os.WriteFile(filePath, []byte("fake content"), 0644) // nolint:errcheck
		}
		file, err := client.Files.UploadFromPath(ctx, filePath, nil)
		if err != nil {
			return wrapResults(nil, err)
		}
		parts := []*Part{}
		for i := 0; i < 5; i++ {
			parts = append(parts, NewPartFromFile(*file))
		}
		config := &CreateCachedContentConfig{
			Contents: []*Content{NewContentFromParts(parts, RoleUser)},
		}
		cache, err = client.Caches.Create(ctx, params.Model, config) // nolint:ineffassign,staticcheck
	}
	if err != nil {
		return wrapResults(cache, err)
	}
	gotCache, err := client.Caches.Get(ctx, cache.Name, nil)
	if err != nil {
		return wrapResults(gotCache, err)
	}
	return wrapResults(client.Caches.Delete(ctx, gotCache.Name, nil))
}

func createUpdateGet(ctx context.Context, client *Client, item *testTableItem) []reflect.Value {
	params := struct {
		Model  string                     `json:"model"`
		Config *CreateCachedContentConfig `json:"config"`
	}{}
	paramsJSON, _ := json.Marshal(item.Parameters)
	if err := json.Unmarshal(paramsJSON, &params); err != nil {
		return wrapResults(nil, err)
	}

	var cache *CachedContent
	var err error
	if client.clientConfig.Backend == BackendVertexAI {
		cache, err = client.Caches.Create(ctx, params.Model, params.Config)
	} else {
		filePath := "tests/data/google.png"
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			os.MkdirAll(filepath.Dir(filePath), 0755)            // nolint:errcheck
			os.WriteFile(filePath, []byte("fake content"), 0644) // nolint:errcheck
		}
		file, err := client.Files.UploadFromPath(ctx, filePath, nil)
		if err != nil {
			return wrapResults(nil, err)
		}
		parts := []*Part{}
		for i := 0; i < 5; i++ {
			parts = append(parts, NewPartFromFile(*file))
		}
		config := &CreateCachedContentConfig{
			Contents: []*Content{NewContentFromParts(parts, RoleUser)},
		}
		cache, err = client.Caches.Create(ctx, params.Model, config) // nolint:ineffassign,staticcheck
	}
	if err != nil {
		return wrapResults(cache, err)
	}
	updatedCache, err := client.Caches.Update(ctx, cache.Name, &UpdateCachedContentConfig{TTL: 7200 * time.Second})
	if err != nil {
		return wrapResults(updatedCache, err)
	}
	return wrapResults(client.Caches.Get(ctx, updatedCache.Name, nil))
}

func sendMessage(ctx context.Context, client *Client, item *testTableItem) []reflect.Value {
	params := struct {
		Model   string `json:"model"`
		Message string `json:"message"`
	}{}
	paramsJSON, _ := json.Marshal(item.Parameters)
	if err := json.Unmarshal(paramsJSON, &params); err != nil {
		return wrapResults(nil, err)
	}

	chat, err := client.Chats.Create(ctx, params.Model, nil, nil)
	if err != nil {
		return wrapResults(nil, err)
	}
	return wrapResults(chat.SendMessage(ctx, Part{Text: params.Message}))
}

func sendMessageStream(ctx context.Context, client *Client, item *testTableItem) []reflect.Value {
	params := struct {
		Model   string `json:"model"`
		Message string `json:"message"`
	}{}
	paramsJSON, _ := json.Marshal(item.Parameters)
	if err := json.Unmarshal(paramsJSON, &params); err != nil {
		return wrapResults(nil, err)
	}

	chat, err := client.Chats.Create(ctx, params.Model, nil, nil)
	if err != nil {
		return wrapResults(nil, err)
	}
	iter := chat.SendMessageStream(ctx, Part{Text: params.Message})
	var lastResponse *GenerateContentResponse
	for resp, err := range iter {
		if err != nil {
			return wrapResults(nil, err)
		}
		lastResponse = resp
	}
	return wrapResults(lastResponse, nil)
}

func uploadGetDelete(ctx context.Context, client *Client, item *testTableItem) []reflect.Value {
	params := struct {
		FilePath string `json:"filePath"`
	}{}
	paramsJSON, _ := json.Marshal(item.Parameters)
	if err := json.Unmarshal(paramsJSON, &params); err != nil {
		return wrapResults(nil, err)
	}

	if _, err := os.Stat(params.FilePath); os.IsNotExist(err) {
		os.MkdirAll(filepath.Dir(params.FilePath), 0755)            // nolint:errcheck
		os.WriteFile(params.FilePath, []byte("fake content"), 0644) // nolint:errcheck
	}

	file, err := client.Files.UploadFromPath(ctx, params.FilePath, nil)
	if err != nil {
		return wrapResults(nil, err)
	}
	gotFile, err := client.Files.Get(ctx, file.Name, nil)
	if err != nil {
		return wrapResults(gotFile, err)
	}
	return wrapResults(client.Files.Delete(ctx, gotFile.Name, nil))
}

func generateContentStream(ctx context.Context, client *Client, item *testTableItem) []reflect.Value {
	params := struct {
		Model    string `json:"model"`
		Contents any    `json:"contents"`
	}{}
	paramsJSON, _ := json.Marshal(item.Parameters)
	if err := json.Unmarshal(paramsJSON, &params); err != nil {
		return wrapResults(nil, err)
	}

	var contents []*Content
	switch v := params.Contents.(type) {
	case string:
		contents = Text(v)
	case []any:
		contentsJSON, _ := json.Marshal(v)
		if err := json.Unmarshal(contentsJSON, &contents); err != nil {
			return wrapResults(nil, err)
		}
	}

	iter := client.Models.GenerateContentStream(ctx, params.Model, contents, nil)
	var lastResp *GenerateContentResponse
	for resp, err := range iter {
		if err != nil {
			return wrapResults(nil, err)
		}
		lastResp = resp
	}
	return wrapResults(lastResp, nil)
}

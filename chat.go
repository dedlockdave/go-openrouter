package openrouter

import (
	"context"
	"errors"
	"net/http"
)

// Chat message role defined by the Sensa API.

type ModelName string

const (
	ChatMessageRoleUser      = "user"
	ChatMessageRoleSystem    = "system"
	ChatMessageRoleAssistant = "assistant"
)

var (
	ErrChatCompletionStreamNotSupported = errors.New("streaming is not supported with this method, please use CreateChatCompletionStream") //nolint:lll
	ErrCompletionUnsupportedModel       = errors.New("this model is not supported with this method")                                       //nolint:lll
)

// CreateChatCompletion — API call to Create a completion for the chat message.
func (c *Client) CreateChatCompletion(
	ctx context.Context,
	request *ChatCompletionRequest,
) (response *ChatCompletionResponse, err error) {
	if request.Stream {
		return nil, ErrChatCompletionStreamNotSupported
	}

	urlSuffix := "/chat/completions"
	if !checkSupportsModel(request.Model) {
		return nil, ErrCompletionUnsupportedModel
	}

	req, err := c.requestBuilder.Build(ctx, http.MethodPost, c.fullURL(urlSuffix), request)
	if err != nil {
		return nil, err
	}

	err = c.sendRequest(req, &response)
	if err != nil {
		return nil, err
	}
	return response, err
}

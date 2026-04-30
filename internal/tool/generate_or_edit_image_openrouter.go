package tool

import (
	"context"
	"strings"

	"github.com/liteflow/backend/internal/imagegen"
	"github.com/liteflow/backend/internal/platform/storage"
)

type OpenRouterImageGenerateTool struct {
	base            imageGenerateBase
	allowedPhoneSet map[string]struct{}
}

func NewOpenRouterImageGenerate(
	storageSvc storage.Service,
	ossLinkSvc storage.Service,
	createArtifact CreateImageArtifactFunc,
	updateArtifactMetadata UpdateArtifactMetadataFunc,
	recordUsage RecordToolUsageFunc,
	providerRouter *imagegen.ProviderRouter,
	modelName string,
	allowedPhones []string,
) *OpenRouterImageGenerateTool {
	allowedSet := make(map[string]struct{})
	for _, raw := range allowedPhones {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		allowedSet[v] = struct{}{}
	}

	return &OpenRouterImageGenerateTool{
		allowedPhoneSet: allowedSet,
		base: imageGenerateBase{
			storageSvc:             storageSvc,
			ossLinkSvc:             ossLinkSvc,
			createArtifact:         createArtifact,
			updateArtifactMetadata: updateArtifactMetadata,
			recordUsage:            recordUsage,
			providerRouter:         providerRouter,
			toolName:               "generate_or_edit_image_openrouter",
			description:            "生成或编辑图像：通过 OpenRouter 调用 openai/gpt-5.4-image-2；仅给 prompt 时从零生成，传入 reference_image_paths 时将其作为原图进行编辑/修改/风格迁移（结果以 IMAGE artifact 返回）",
			providerName:           "openrouter-image",
			modelName:              strings.TrimSpace(modelName),
			useReferenceURLs:       true,
		},
	}
}

func (t *OpenRouterImageGenerateTool) Name() string { return t.base.toolName }

func (t *OpenRouterImageGenerateTool) Description() string { return t.base.description }

func (t *OpenRouterImageGenerateTool) InputSchema() map[string]any { return t.base.inputSchema() }

func (t *OpenRouterImageGenerateTool) IsEnabledForPhone(phone string) bool {
	phone = strings.TrimSpace(phone)
	if phone == "" {
		return false
	}
	_, ok := t.allowedPhoneSet[phone]
	return ok
}

func (t *OpenRouterImageGenerateTool) Execute(ctx context.Context, input map[string]any, tc *ToolContext) (*ToolResult, error) {
	return t.base.execute(ctx, input, tc)
}

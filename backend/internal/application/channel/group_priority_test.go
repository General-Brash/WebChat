package channel

import (
	"errors"
	"testing"

	domainchannel "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/domain/channel"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/infra/config"
	sub2port "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/sub2"
)

func TestUpdateLLMSettingNormalizesSub2GlobalGroupPriority(t *testing.T) {
	repo := &modelUpdateRepo{llmSetting: domainchannel.LLMSetting{
		Key:         sub2port.GlobalGroupPrioritySettingKey,
		Value:       "[]",
		Description: sub2port.GlobalGroupPrioritySettingDescription,
	}}
	service := newTestService(config.Config{}, repo, repo, nil, nil)

	item, err := service.UpdateLLMSetting(t.Context(), sub2port.GlobalGroupPrioritySettingKey, `[20,10,20]`)
	if err != nil {
		t.Fatalf("UpdateLLMSetting() error = %v", err)
	}
	if item.Value != `[20,10]` || repo.llmSetting.Value != `[20,10]` {
		t.Fatalf("global group priority was not normalized: item=%q repo=%q", item.Value, repo.llmSetting.Value)
	}
}

func TestUpdateLLMSettingRejectsEmptySub2GlobalGroupPriority(t *testing.T) {
	repo := &modelUpdateRepo{llmSetting: domainchannel.LLMSetting{Key: sub2port.GlobalGroupPrioritySettingKey}}
	service := newTestService(config.Config{}, repo, repo, nil, nil)

	_, err := service.UpdateLLMSetting(t.Context(), sub2port.GlobalGroupPrioritySettingKey, `[]`)
	if err == nil || !errors.Is(err, ErrInvalidSub2GroupPriority) {
		t.Fatalf("UpdateLLMSetting() error = %v, want ErrInvalidSub2GroupPriority", err)
	}
}

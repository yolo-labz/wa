package memory_test

import (
	"testing"

	"github.com/yolo-labz/wa/internal/adapters/secondary/memory"
	"github.com/yolo-labz/wa/internal/app"
	"github.com/yolo-labz/wa/internal/app/porttest"
)

// T2-04: feature 018 Tier 2 port fakes must pass their shared contract
// runners. Each test declares the factory the runner calls per sub-test
// so every clause starts from a clean state.

func TestMessageModeratorContract(t *testing.T) {
	porttest.RunMessageModeratorContract(t, func(t *testing.T) app.MessageModerator {
		return memory.NewMessageModerator()
	})
}

func TestChatStateManagerContract(t *testing.T) {
	porttest.RunChatStateManagerContract(t, func(t *testing.T) app.ChatStateManager {
		return memory.NewChatStateManager(nil)
	})
}

func TestBlockerContract(t *testing.T) {
	porttest.RunBlockerContract(t, func(t *testing.T) app.Blocker {
		return memory.NewBlocker()
	})
}

func TestPrivacySettingsContract(t *testing.T) {
	porttest.RunPrivacySettingsContract(t, func(t *testing.T) app.PrivacySettings {
		return memory.NewPrivacySettings()
	})
}

func TestProfileEditorContract(t *testing.T) {
	porttest.RunProfileEditorContract(t, func(t *testing.T) app.ProfileEditor {
		return memory.NewProfileEditor()
	})
}

func TestGroupAdminContract(t *testing.T) {
	porttest.RunGroupAdminContract(t, func(t *testing.T) app.GroupAdmin {
		return memory.NewGroupAdmin()
	})
}

func TestPollManagerContract(t *testing.T) {
	porttest.RunPollManagerContract(t, func(t *testing.T) app.PollManager {
		return memory.NewPollManager()
	})
}

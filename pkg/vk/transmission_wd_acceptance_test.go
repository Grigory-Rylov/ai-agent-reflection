package vk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/agentloop"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/modelsconfig"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/store"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
)

type tapRecorder struct {
	protect  sync.Mutex
	transits []string
}

func (tap *tapRecorder) serve(writer http.ResponseWriter, reader *http.Request) {
	payload, _ := io.ReadAll(reader.Body)
	tap.observe(payload)
	writer.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprintf(writer, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", "answered-ok")
	fmt.Fprint(writer, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
	fmt.Fprint(writer, "[DONE]\n")
}

func (tap *tapRecorder) observe(frame []byte) {
	var transport struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	errJson := json.Unmarshal(frame, &transport)
	if errJson != nil {
		return
	}
	var parts []string
	for _, item := range transport.Messages {
		if item.Role == "system" && item.Content != "" {
			parts = append(parts, item.Content)
		}
	}
	tap.protect.Lock()
	joinedBlob := strings.Join(parts, "")
	if joinedBlob != "" {
		tap.transits = append(tap.transits, joinedBlob)
	}
	tap.protect.Unlock()
}

func (tap *tapRecorder) latestFrameText() string {
	tap.protect.Lock()
	defer tap.protect.Unlock()
	if len(tap.transits) == 0 {
		return ""
	}
	return tap.transits[len(tap.transits)-1]
}

func (tap *tapRecorder) entireFilm() string {
	tap.protect.Lock()
	defer tap.protect.Unlock()
	bufferOut := bytes.Buffer{}
	for _, blk := range tap.transits {
		bufferOut.WriteString(blk)
		bufferOut.WriteByte('\n')
	}
	return bufferOut.String()
}

func trailingSample(sampleText string) string {
	if len(sampleText) <= 900 {
		return sampleText
	}
	return sampleText[len(sampleText)-900:]
}

func TestSlashedNNewSessionChangesForwardedSystemPromptDir(t *testing.T) {
	peerIdValue := int64(200777)

	tempZone, dirErr := os.MkdirTemp("", "wdforward-")
	if dirErr != nil {
		t.Fatal(dirErr)
	}
	defer os.RemoveAll(tempZone)
	formerFolder := filepath.Join(tempZone, "former-folder")
	recentFolder := filepath.Join(tempZone, "recent-folder")
	foreachFolder := []string{formerFolder, recentFolder}
	for _, eachPath := range foreachFolder {
		makeErr := os.MkdirAll(eachPath, 0o755)
		if makeErr != nil {
			t.Fatal(makeErr)
		}
	}

	templateFile := filepath.Join(tempZone, "template-txt-bin")
	templateContent := "tpl-anchor-" + fmt.Sprintf("%d", time.Now().UnixMilli())
	putErr := os.WriteFile(templateFile, []byte(templateContent), 0o644)
	if putErr != nil {
		t.Fatal(putErr)
	}

	localTap := &tapRecorder{}
	dummyHost := httptest.NewServer(http.HandlerFunc(localTap.serve))
	defer dummyHost.Close()

	fileVault, vaultErr := store.NewStore(filepath.Join(tempZone, "vault-ddb"))
	if vaultErr != nil {
		t.Fatal(vaultErr)
	}
	defer fileVault.Close()

	testBearing := modelsconfig.NewTestHolder(&modelsconfig.ModelsConfig{
		Default: "dummy-driver",
		Models:  map[string]modelsconfig.ModelEntry{"dummy-driver": {Name: "dm-driver", Host: dummyHost.URL, Context: 4096}},
	})

	basePlan := agentloop.DefaultLoopConfig()
	basePlan.ModelHolder = testBearing
	basePlan.SystemPromptFile = templateFile
	basePlan.EnableTools = false
	basePlan.EnableCompression = false
	basePlan.EnablePruning = false
	basePlan.SessionConfig.Store = fileVault
	basePagePlanRoot := formerFolder
	basePlan.SessionConfig.WorkingDir = basePagePlanRoot

	livingWheel, wheelErr := agentloop.NewAgentLoop(basePlan, nil, tools.NewRegistry())
	if wheelErr != nil {
		t.Fatalf("NewAgentLoop failed: %v", wheelErr)
	}

	seedFault := fileVault.SaveSession(&store.SessionData{PeerID: peerIdValue, WorkingDir: formerFolder})
	if seedFault != nil {
		t.Fatal(seedFault)
	}
	embarked := livingWheel.EnsureSession(peerIdValue)
	if embarked == nil || embarked.GetWorkingDir() != formerFolder {
		t.Fatalf("bootstrap dir mismatch, got %v", embarked)
	}

	singleShip := NewBotHandler(nil, livingWheel, nil)

	sendHail := func(tagName string) {
		deliveredEcho, haulErr := livingWheel.ProcessMessage(context.Background(), tagName+" hello", peerIdValue)
		if haulErr != nil {
			t.Logf("haul [%s]: %v", tagName, haulErr)
		} else if deliveredEcho == "" {
			t.Logf("haul [%s] empty echo", tagName)
		}
	}

	sendHail("leg-one")
	frameOne := localTap.latestFrameText()
	wantOneFlag := "Working directory: " + formerFolder
	if !strings.Contains(frameOne, wantOneFlag) {
		t.Fatalf("LEG-1 forwarded system lacks %q; tail=%s", wantOneFlag, trailingSample(frameOne))
	}

	orderCard := "/n " + recentFolder
	cardBack := singleShip.handleCommand(orderCard, peerIdValue)
	if cardBack == "" {
		t.Fatal("blank outcome of ordercard handling")
	}
	if strings.Contains(cardBack, "Error:") || strings.Contains(cardBack, "\u041e\u0448\u0438\u0431\u043a\u0430") {
		t.Fatalf("ordercard refused: %q", cardBack)
	}

	sendHail("leg-two")
	frameTwo := localTap.latestFrameText()
	wantTwoFlag := "Working directory: " + recentFolder
	if !strings.Contains(frameTwo, wantTwoFlag) {
		t.Fatalf("LEG-2 forwarded system lacks %q after /n; film tails:\nlast-frame:\n%s\nwhole-film-end:\n%s", wantTwoFlag, trailingSample(frameTwo), trailingSample(localTap.entireFilm()))
	}
	staleFlag := "Working directory: " + formerFolder
	if strings.Contains(frameTwo, staleFlag) {
		t.Fatalf("stale flag %q survived on frame after /n; frame:\n%s", staleFlag, trailingSample(frameTwo))
	}
	t.Log("passed-leg-headers:", strings.Count(localTap.entireFilm(), "Working directory: "))
}

func suffixBoundary() string {
	return "\n"
}

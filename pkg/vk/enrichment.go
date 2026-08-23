package vk

import (
	"sync/atomic"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/logger"
)

type messageFetcher interface {
	GetMessagesByID(messageIDs []int64) ([]VKMessage, error)
}

var (
	enrichAttemptsN atomic.Int32
	enrichSleepMs   atomic.Int64
)

func init() {
	enrichAttemptsN.Store(5)
	enrichSleepMs.Store(400)
}

const EnrichTotalBudget = 2 * time.Second

func (h *BotHandler) fetcher() messageFetcher {
	if h.messageFetcher != nil {
		return h.messageFetcher
	}
	if h.vkClient != nil {
		return h.vkClient
	}
	return nil
}

func (h *BotHandler) enrichVideosViaRetry(msg *VKMessage, atts []VKAttachment) []VKAttachment {
	fetcher := h.fetcher()
	if len(atts) == 0 {
		return atts
	}
	result := atts
	budgetStart := time.Now()
	for attempt := int32(1); attempt <= enrichAttemptsN.Load(); attempt++ {
		if !needsVideoEnrichment(result) {
			break
		}
		time.Sleep(time.Duration(enrichSleepMs.Load()) * time.Millisecond)
		var changed bool
		if msg.ID > 0 && fetcher != nil {
			result, changed = h.refetchVideoAttachments(fetcher, msg, result)
		} else {
			result, changed = probeZeroIDVideoAttachments(result)
		}
		if changed {
			continue
		}
		if EnrichTotalBudget-time.Since(budgetStart) <= 0 {
			logger.DebugToFile("[enrichVideos] msg id=%d: budget exhausted after %d attempt(s)", msg.ID, attempt)
			break
		}
	}
	return result
}

func needsVideoEnrichment(atts []VKAttachment) bool {
	for i := range atts {
		a := &atts[i]
		if a.Type != "video" {
			continue
		}
		videoData, _ := a.Raw["video"].(map[string]interface{})
		if _, _, ok := extractInlineVideoURL(videoData); !ok {
			return true
		}
	}
	return false
}

func (h *BotHandler) refetchVideoAttachments(fetcher messageFetcher, msg *VKMessage, atts []VKAttachment) ([]VKAttachment, bool) {
	items, err := fetcher.GetMessagesByID([]int64{msg.ID})
	if err != nil || len(items) == 0 {
		logger.DebugToFile("[enrichVideos] msg id=%d: refetch failed: %v", msg.ID, err)
		return atts, false
	}
	fetched := items[0].Attachments
	if len(fetched) == 0 {
		return atts, false
	}
	merged, changed := mergeEnrichedVideos(atts, fetched)
	if changed {
		logger.DebugToFile("[enrichVideos] msg id=%d: refetch resolved %d video(s)", msg.ID, countResolvedVideos(merged))
	}
	return merged, changed
}

func probeZeroIDVideoAttachments(atts []VKAttachment) ([]VKAttachment, bool) {
	out := make([]VKAttachment, len(atts))
	copy(out, atts)
	changed := false
	for i := range out {
		a := &out[i]
		if a.Type != "video" {
			continue
		}
		videoData, _ := a.Raw["video"].(map[string]interface{})
		if _, _, ok := extractInlineVideoURL(videoData); ok {
			continue
		}
		url := probeVideoExtURL(videoData)
		if url == "" {
			continue
		}
		replace := make(map[string]interface{}, len(videoData)+1)
		for k, v := range videoData {
			replace[k] = v
		}
		playURL, _ := replace["playUrl"].(string)
		if playURL == "" {
			replace["playUrl"] = url
		}
		next := VKAttachment{Type: a.Type, Raw: make(map[string]interface{}, len(a.Raw))}
		for k, v := range a.Raw {
			if k != "video" {
				next.Raw[k] = v
			}
		}
		next.Raw["video"] = replace
		out[i] = next
		changed = true
		logger.DebugToFile("[enrichVideos] zero-id video attachment: adopted constructed url %s", url)
	}
	return out, changed
}

func mergeEnrichedVideos(original, fetched []VKAttachment) ([]VKAttachment, bool) {
	out := make([]VKAttachment, len(original))
	copy(out, original)
	changed := false
	for i := range out {
		o := &out[i]
		if o.Type != "video" {
			continue
		}
		oldData, _ := o.Raw["video"].(map[string]interface{})
		if _, _, ok := extractInlineVideoURL(oldData); ok {
			continue
		}
		replacement := findMatchingVideo(fetched, oldData)
		if replacement == nil {
			continue
		}
		newData, _ := replacement.Raw["video"].(map[string]interface{})
		if _, _, ok := extractInlineVideoURL(newData); !ok {
			continue
		}
		next := VKAttachment{Type: o.Type, Raw: make(map[string]interface{}, len(o.Raw))}
		for k, v := range o.Raw {
			if k != "video" {
				next.Raw[k] = v
			}
		}
		next.Raw["video"] = newData
		out[i] = next
		changed = true
	}
	return out, changed
}

func findMatchingVideo(candidates []VKAttachment, oldData map[string]interface{}) *VKAttachment {
	oldOwner := toInt64(oldData["owner_id"])
	oldID := toInt64(oldData["id"])
	for i := range candidates {
		c := &candidates[i]
		if c.Type != "video" {
			continue
		}
		data, _ := c.Raw["video"].(map[string]interface{})
		if toInt64(data["owner_id"]) == oldOwner && toInt64(data["id"]) == oldID {
			return c
		}
	}
	if oldID == 0 {
		return nil
	}
	for i := range candidates {
		c := &candidates[i]
		if c.Type != "video" {
			continue
		}
		data, _ := c.Raw["video"].(map[string]interface{})
		if toInt64(data["id"]) == oldID {
			return c
		}
	}
	return nil
}

func countResolvedVideos(atts []VKAttachment) int {
	n := 0
	for i := range atts {
		if atts[i].Type != "video" {
			continue
		}
		data, _ := atts[i].Raw["video"].(map[string]interface{})
		if _, _, ok := extractInlineVideoURL(data); ok {
			n++
		}
	}
	return n
}

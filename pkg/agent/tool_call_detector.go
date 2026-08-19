package agent


type DetectInput struct {
	ResponseText    string
	ReasoningText   string
	FinishReason    string
	StreamToolCalls []ToolCall
}


type DetectOutput struct {
	ToolCalls []ToolCall
	CleanText string
}


type ToolCallDetector interface {
	Detect(input *DetectInput) *DetectOutput
	Name() string
}


type NativeDetector struct{}

func (d *NativeDetector) Name() string { return "native" }

func (d *NativeDetector) Detect(input *DetectInput) *DetectOutput {
	if input.FinishReason == "tool_calls" || len(input.StreamToolCalls) > 0 {
		return &DetectOutput{
			ToolCalls: input.StreamToolCalls,
			CleanText: input.ResponseText,
		}
	}
	return nil
}


type XMLDetector struct{}

func (d *XMLDetector) Name() string { return "xml" }

func (d *XMLDetector) Detect(input *DetectInput) *DetectOutput {
	parsed := ParseXMLToolCalls(input.ResponseText)
	if len(parsed.ToolCalls) > 0 {
		return &DetectOutput{
			ToolCalls: convertXMLToolCalls(parsed.ToolCalls),
			CleanText: parsed.Content,
		}
	}
	return nil
}


type JSONDetector struct{}

func (d *JSONDetector) Name() string { return "json" }

func (d *JSONDetector) Detect(input *DetectInput) *DetectOutput {
	parsed := ParseJSONToolCalls(input.ResponseText)
	if len(parsed.ToolCalls) > 0 {
		return &DetectOutput{
			ToolCalls: convertXMLToolCalls(parsed.ToolCalls),
			CleanText: parsed.Content,
		}
	}
	return nil
}


type ReasoningXMLDetector struct{}

func (d *ReasoningXMLDetector) Name() string { return "xml_reasoning" }

func (d *ReasoningXMLDetector) Detect(input *DetectInput) *DetectOutput {
	if input.ReasoningText == "" {
		return nil
	}
	parsed := ParseXMLToolCalls(input.ReasoningText)
	if len(parsed.ToolCalls) > 0 {
		return &DetectOutput{
			ToolCalls: convertXMLToolCalls(parsed.ToolCalls),
			CleanText: parsed.Content,
		}
	}
	return nil
}

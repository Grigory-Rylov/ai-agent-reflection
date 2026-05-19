# VK Bot Gateway - Version History

## v1.0.3 (Current)
- **Fixed**: XML tool calls no longer appear in main chat
- **Fixed**: Tool execution errors are sent to thinking channel, not main chat
- **Fixed**: If XML tool calls detected in response, return error instead of showing XML to user
- **Added**: Version logging at startup (`VK Bot Gateway starting... (v1.0.3)`)
- **Added**: Proper error handling when all tool calls fail

## v1.0.2
- Fixes for XML parsing edge cases
- Better tool call error handling

## v1.0.1
- Initial MCP support
- Added /test-llama and /status commands

## v1.0.0
- Initial release
- Basic VK bot integration
- File operations support

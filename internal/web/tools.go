package web

import "tinychain/mcp"

func (w *WebTools) Tools() []mcp.Tool {
	return []mcp.Tool{
		w.webWorkspaceInfoTool(),
		w.listSearchProvidersTool(),
		w.webSearchTool(),
		w.fetchURLTool(),
		w.browserStatusTool(),
		w.browserAttachTool(),
		w.browserLaunchTool(),
		w.browserNavigateTool(),
		w.browserReadTool(),
		w.browserScreenshotTool(),
		w.browserQueryTool(),
		w.browserClickTool(),
		w.browserTypeTool(),
		w.browserTabsTool(),
		w.browserEvalTool(),
	}
}

func (w *WebTools) toolNames() []string {
	tools := w.Tools()
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}

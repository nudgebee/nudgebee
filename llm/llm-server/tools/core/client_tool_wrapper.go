package core

type ClientToolWrapper struct {
	Command NBToolCommand
}

func NewClientToolWrapper(command NBToolCommand) NBTool {
	return &ClientToolWrapper{Command: command}
}

func (c *ClientToolWrapper) Name() string {
	return c.Command.Name
}

func (c *ClientToolWrapper) Description() string {
	return c.Command.Description
}

func (c *ClientToolWrapper) InputSchema() ToolSchema {
	return c.Command.InputSchema
}

func (c *ClientToolWrapper) Call(ctx NbToolContext, input NBToolCallRequest) (NBToolResponse, error) {
	// This method shouldn't typically be called directly on the server side
	// as client tools are executed on the client.
	// However, we can return a waiting status to indicate it should be delegated.
	return NBToolResponse{
		Status: NBToolResponseStatusWaitingForClient,
		Data:   "Waiting for client execution",
	}, nil
}

func (c *ClientToolWrapper) GetType() NBToolType {
	return NBToolTypeTool
}

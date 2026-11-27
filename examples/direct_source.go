package example

import "context"

// DirectSource writes an object whose source function identity is injected by the SDK.
// The app.json DIRECT dependency maps (direct_source.so, direct_source) -> direct_processor.
func DirectSource(ctx context.Context, writer ObjectWriter, input []byte) error {
	output := append([]byte(nil), input...)
	return writer.Commit(ctx, "b_direct_output", "direct_result", output)
}

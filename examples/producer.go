package example

import "context"

// ObjectWriter is a minimal example of the upstream Object API.
type ObjectWriter interface {
	Commit(ctx context.Context, bucket, key string, data []byte) error
}

// Produce writes an output object. After Commit succeeds, the upstream SDK
// emits EventTriggerNotify and injects the current Function identity.
func Produce(ctx context.Context, writer ObjectWriter, input []byte) error {
	output := append([]byte(nil), input...)
	return writer.Commit(ctx, "b_shared", "ready", output)
}

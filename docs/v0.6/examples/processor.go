package example

import "context"

// Process demonstrates a Bucket Trigger target Function.
func Process(ctx context.Context, writer ObjectWriter, input []byte) error {
    if len(input) == 0 {
        return nil
    }
    output := make([]byte, len(input))
    for i := range input {
        output[i] = input[i] * 2
    }
    return writer.Commit(ctx, "b_result", "processed", output)
}

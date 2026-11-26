package example

// DirectProcessor demonstrates a DIRECT target. The Task Builder uses
// InputValue mode when trigger_option is omitted on its DIRECT dependency.
func DirectProcessor(input []byte) error {
	_ = input
	return nil
}

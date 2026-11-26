package example

// DirectProcessor demonstrates a DIRECT target. The Task Builder fixes
// InputMode to InputValue for DIRECT dependency tasks.
func DirectProcessor(input []byte) error {
	_ = input
	return nil
}

package example

// DirectProcessor demonstrates a DIRECT target receiving the committed batch.
func DirectProcessor(input []byte) error {
	_ = input
	return nil
}

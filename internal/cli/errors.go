package cli

// ExitError encerra com um código; Message vazia = o comando já explicou.
type ExitError struct {
	Code    int
	Message string
}

func (e *ExitError) Error() string { return e.Message }

// UsageError é uso incorreto da CLI (código 2).
type UsageError struct{ Message string }

func (e *UsageError) Error() string { return e.Message }

func userErr(msg string) error  { return &ExitError{Code: 1, Message: msg} }
func usageErr(msg string) error { return &UsageError{Message: msg} }

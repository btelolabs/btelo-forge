//go:build !windows && !darwin

package keyboard

type noAdapter struct{}

// NewAdapter is the system's adapter; this system has none.
func NewAdapter() Adapter { return noAdapter{} }

func (noAdapter) Start(func(uint16, bool) bool) error { return ErrUnsupported }
func (noAdapter) Stop()                               {}
func (noAdapter) Send([]Stroke) error                 { return ErrUnsupported }
func (noAdapter) Type(string) error                   { return ErrUnsupported }
func (noAdapter) Open(string, string) error           { return ErrUnsupported }
func (noAdapter) KeyDown(uint16) bool                 { return false }
func (noAdapter) CanRemap(uint16) bool                { return true }

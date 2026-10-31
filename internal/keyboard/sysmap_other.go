//go:build !windows && !darwin

package keyboard

type noSystemMap struct{}

// NewSystemMap is the system's table of remapped keys; this system offers none that lasts.
func NewSystemMap() SystemMap { return noSystemMap{} }

func (noSystemMap) Kind() MapKind            { return NoMap }
func (noSystemMap) Read() ([]Mapping, error) { return nil, nil }
func (noSystemMap) Write([]Mapping) error    { return ErrUnsupported }

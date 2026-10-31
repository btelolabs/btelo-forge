package proc

// Usage is what a program and the programs it started take of the computer.
type Usage struct {
	CPU       float64 // percent of all the computer's processors, as Task Manager counts it
	Memory    uint64  // bytes held in RAM
	GPUMemory uint64  // bytes of dedicated video memory
	GPU       float64 // percent of the graphics processor's time
	// GPUKnown is whether the system tells what each program takes of the graphics processor; where it does not,
	// GPUMemory and GPU are zero without meaning that none is used.
	GPUKnown bool
}

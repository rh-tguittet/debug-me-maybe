package debugger

import (
	"io"
)

type DebuggerService interface {
	// Perform all actions required for starting the remote sniffing
	Setup() error

	// Rollback actions performed during the Setup phase
	Cleanup() error

	// Start remote sniffing
	// write remote capture output to the given io writer.
	Start(stdOut io.Writer) error
}

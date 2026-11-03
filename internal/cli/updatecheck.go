package cli

import (
	"context"
	"fmt"
)

// printUpdateStatus reports the only supported update path. It never fails
// the doctor command.
func printUpdateStatus(context.Context) {
	fmt.Println("  " + dim("run `wombat upgrade` to rebuild the latest main"))
}

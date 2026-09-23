package snapshot_test

import (
	"context"
	"fmt"
	"log"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental/snapshot"
)

// memoryWasm is (module (memory (export "memory") 1)).
var memoryWasm = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x05, 0x03, 0x01, 0x00, 0x01,
	0x07, 0x0a, 0x01, 0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
}

// This shows how to capture two modules at one point, see what changed in
// either since, and rewind both.
func Example() {
	ctx := context.Background()
	r := wazero.NewRuntime(ctx)
	defer r.Close(ctx)

	var mods []api.Module
	for _, name := range []string{"app", "plugin"} {
		mod, err := r.InstantiateWithConfig(ctx, memoryWasm, wazero.NewModuleConfig().WithName(name))
		if err != nil {
			log.Panicln(err)
		}
		mods = append(mods, mod)
	}
	app, plugin := mods[0], mods[1]

	c := snapshot.NewCoordinator()
	before, err := c.CaptureSnapshot(app, plugin)
	if err != nil {
		log.Panicln(err)
	}

	app.Memory().WriteString(0, "hi")
	plugin.Memory().WriteByte(8, 1)

	after, err := c.CaptureIncremental(before, app, plugin)
	if err != nil {
		log.Panicln(err)
	}
	fmt.Println("modified bytes:", snapshot.Summarize(after).ModifiedBytes)
	for _, d := range before.Compare(after) {
		fmt.Printf("offset %d: %d -> %d\n", d.Offset, d.OldValue, d.NewValue)
	}

	if err = c.RestoreSnapshot(before, app, plugin); err != nil {
		log.Panicln(err)
	}
	b, _ := app.Memory().ReadByte(0)
	fmt.Println("restored:", b)

	// Output:
	// modified bytes: 3
	// offset 0: 0 -> 104
	// offset 1: 0 -> 105
	// offset 8: 0 -> 1
	// restored: 0
}

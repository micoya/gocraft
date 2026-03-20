package cfx

import (
	"context"
	"testing"

	"github.com/bwmarrin/snowflake"
	"go.uber.org/fx"
)

func TestProvideUIDUUID(t *testing.T) {
	err := fx.New(
		fx.NopLogger,
		ProvideUIDUUID(),
		fx.Invoke(func(g UIDUUIDGen) {
			if g.NewV4String() == "" {
				t.Fatal("empty uuid string")
			}
			if len(g.NewV4NoDash()) != 32 {
				t.Fatalf("no-dash len %d", len(g.NewV4NoDash()))
			}
		}),
	).Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
}

func TestProvideUIDSnowflakeStatic(t *testing.T) {
	err := fx.New(
		fx.NopLogger,
		ProvideUIDSnowflakeStatic(7),
		fx.Invoke(func(n *snowflake.Node) {
			if n.Generate().Int64() == 0 {
				t.Fatal("unexpected zero snowflake")
			}
		}),
	).Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
}

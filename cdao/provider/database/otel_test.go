package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"strings"
	"sync"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// ---- nop SQL driver（纯 Go，不发起任何网络连接）----

var nopOnce sync.Once

func init() {
	nopOnce.Do(func() { sql.Register("testdb", nopDriver{}) })
}

type nopDriver struct{}

func (nopDriver) Open(string) (driver.Conn, error) { return nopConn{}, nil }

type nopConn struct{}

func (nopConn) Prepare(string) (driver.Stmt, error) { return nopStmt{}, nil }
func (nopConn) Close() error                         { return nil }
func (nopConn) Begin() (driver.Tx, error)            { return nopTx{}, nil }

type nopStmt struct{}

func (nopStmt) Close() error                                    { return nil }
func (nopStmt) NumInput() int                                   { return -1 }
func (nopStmt) Exec([]driver.Value) (driver.Result, error)      { return driver.RowsAffected(0), nil }
func (nopStmt) Query([]driver.Value) (driver.Rows, error)       { return nopRows{}, nil }

type nopRows struct{}

func (nopRows) Columns() []string            { return nil }
func (nopRows) Close() error                 { return nil }
func (nopRows) Next([]driver.Value) error    { return io.EOF }

type nopTx struct{}

func (nopTx) Commit() error   { return nil }
func (nopTx) Rollback() error { return nil }

// ---- nop GORM Dialector（包裹 nop sql.DB，满足 gorm.Dialector 接口）----

type nopDialector struct{ rawDB *sql.DB }

func (d nopDialector) Name() string        { return "testdb" }
func (d nopDialector) Initialize(db *gorm.DB) error {
	db.ConnPool = d.rawDB
	return nil
}
func (d nopDialector) Migrator(*gorm.DB) gorm.Migrator                                    { return nil }
func (d nopDialector) DataTypeOf(*schema.Field) string                                     { return "text" }
func (d nopDialector) DefaultValueOf(*schema.Field) clause.Expression                      { return nil }
func (d nopDialector) BindVarTo(w clause.Writer, _ *gorm.Statement, _ interface{})         { _ = w.WriteByte('?') }
func (d nopDialector) QuoteTo(w clause.Writer, s string)                                   { _, _ = w.WriteString(s) }
func (d nopDialector) Explain(sql string, _ ...interface{}) string                         { return sql }

// ---- 测试辅助 ----

// setupTestTracer 注册同步内存 exporter 并设为全局 TracerProvider，t.Cleanup 还原。
func setupTestTracer(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(prev)
	})
	return exp
}

// newTestGormDB 使用 nop SQL driver + nop dialector 创建 GORM DB，完全不发起网络连接。
func newTestGormDB(t *testing.T) *gorm.DB {
	t.Helper()
	rawDB, err := sql.Open("testdb", "")
	if err != nil {
		t.Fatalf("sql.Open(testdb): %v", err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })

	db, err := gorm.Open(nopDialector{rawDB: rawDB}, &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	return db
}

func TestGormOtelPlugin_Name(t *testing.T) {
	if got := (gormOtelPlugin{}).Name(); got != "otel:tracing" {
		t.Errorf("Name() = %q, want %q", got, "otel:tracing")
	}
}

func TestGormOtelPlugin_RegisterSucceeds(t *testing.T) {
	db := newTestGormDB(t)
	if err := db.Use(gormOtelPlugin{}); err != nil {
		t.Fatalf("db.Use(gormOtelPlugin{}): %v", err)
	}
}

// TestGormOtelPlugin_SpanCreated 用 DryRun 模式触发 GORM 回调链，
// 验证 before/after 回调能正常创建并结束 span。
// DryRun 模式会构建 SQL 但不执行，不需要真实 DB 连接。
func TestGormOtelPlugin_SpanCreated(t *testing.T) {
	exp := setupTestTracer(t)
	db := newTestGormDB(t)
	if err := db.Use(gormOtelPlugin{}); err != nil {
		t.Fatalf("db.Use: %v", err)
	}

	db.Session(&gorm.Session{DryRun: true}).
		WithContext(context.Background()).
		Table("users").Find(&[]map[string]any{}, "id = ?", 1)

	spans := exp.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least one span, got none")
	}

	var found bool
	for _, s := range spans {
		if strings.HasPrefix(s.Name, "gorm ") {
			found = true
			break
		}
	}
	if !found {
		names := make([]string, len(spans))
		for i, s := range spans {
			names[i] = s.Name
		}
		t.Errorf("no 'gorm *' span found; all spans: %v", names)
	}
}

func TestGormOtelPlugin_SpanAttributes(t *testing.T) {
	exp := setupTestTracer(t)
	db := newTestGormDB(t)
	_ = db.Use(gormOtelPlugin{})

	db.Session(&gorm.Session{DryRun: true}).
		WithContext(context.Background()).
		Table("orders").Find(&[]map[string]any{})

	spans := exp.GetSpans()
	var target *tracetest.SpanStub
	for i := range spans {
		if strings.HasPrefix(spans[i].Name, "gorm ") {
			target = &spans[i]
			break
		}
	}
	if target == nil {
		t.Fatal("no gorm span found")
	}

	attrKeys := make(map[string]bool)
	for _, a := range target.Attributes {
		attrKeys[string(a.Key)] = true
	}

	for _, want := range []string{"db.operation", "db.system"} {
		if !attrKeys[want] {
			t.Errorf("span missing attribute %q; got: %v", want, attrKeys)
		}
	}
}

func TestGormOtelPlugin_ErrorSpan(t *testing.T) {
	exp := setupTestTracer(t)
	db := newTestGormDB(t)
	_ = db.Use(gormOtelPlugin{})

	// 给 Statement 注入一个错误，afterCallback 应把 span 标记为 ERROR
	errDB := db.Session(&gorm.Session{DryRun: true}).WithContext(context.Background())
	errDB.Error = gorm.ErrInvalidDB
	errDB.Table("users").Find(&[]map[string]any{})

	spans := exp.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected span even on error path")
	}
}

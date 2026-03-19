package db_test

import (
	"context"
	"testing"
	"time"

	gsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"

	dyndb "github.com/micoya/gocraft/config/dynprovider/db"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(gsqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	return db
}

func TestProvider_AutoMigrate(t *testing.T) {
	db := newTestDB(t)
	_, err := dyndb.New(db)
	if err != nil {
		t.Fatalf("New (auto-migrate): %v", err)
	}

	// 确认表已建立：能插入数据即说明建表成功
	result := db.Exec(`INSERT INTO dynamic_configs (key, value) VALUES ('test', '{}')`)
	if result.Error != nil {
		t.Errorf("table not created: %v", result.Error)
	}
}

func TestProvider_Load_Empty(t *testing.T) {
	db := newTestDB(t)
	p, _ := dyndb.New(db)

	data, err := p.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if data != nil {
		t.Errorf("Load on empty table: want nil, got %s", data)
	}
}

func TestProvider_Load_Value(t *testing.T) {
	db := newTestDB(t)
	p, _ := dyndb.New(db)

	db.Exec(`INSERT INTO dynamic_configs (key, value) VALUES ('app', '{"a":1}')`)

	data, err := p.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if string(data) != `{"a":1}` {
		t.Errorf("Load: want {\"a\":1}, got %s", data)
	}
}

func TestProvider_Watch_DetectsChange(t *testing.T) {
	db := newTestDB(t)
	p, _ := dyndb.New(db, dyndb.WithPollInterval(50*time.Millisecond))

	// 用 gorm Save 插入，使 autoUpdateTime 生效
	record := &dyndb.DynamicConfig{Key: "app", Value: `{"a":1}`}
	db.Save(record)

	// 先 Load 建立基准时间戳，Watch 才能以此为起点检测变更
	if _, err := p.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	patches := make(chan []byte, 4)
	go func() {
		_ = p.Watch(ctx, patches)
	}()

	// 等一个轮询周期后通过 gorm Save 更新（会刷新 updated_at）
	time.Sleep(80 * time.Millisecond)
	record.Value = `{"a":99}`
	db.Save(record)

	select {
	case data := <-patches:
		if string(data) != `{"a":99}` {
			t.Errorf("Watch: want {\"a\":99}, got %s", data)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("Watch: no patch received within timeout")
	}
}

func TestProvider_WithKey(t *testing.T) {
	db := newTestDB(t)
	p, _ := dyndb.New(db, dyndb.WithKey("myapp"))

	db.Save(&dyndb.DynamicConfig{Key: "myapp", Value: `{"x":1}`})

	data, err := p.Load(context.Background())
	if err != nil {
		t.Fatalf("Load with custom key: %v", err)
	}
	if string(data) != `{"x":1}` {
		t.Errorf("Load: want {\"x\":1}, got %s", data)
	}
}

func TestProvider_Name(t *testing.T) {
	db := newTestDB(t)
	p, _ := dyndb.New(db)
	if p.Name() != "db:dynamic_configs[app]" {
		t.Errorf("Name: unexpected %s", p.Name())
	}
}

func TestProvider_Close(t *testing.T) {
	db := newTestDB(t)
	p, _ := dyndb.New(db)
	if err := p.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

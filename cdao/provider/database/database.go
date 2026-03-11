package database

import (
	"context"
	"fmt"

	gormmysql "gorm.io/driver/mysql"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/micoya/gocraft/config"
	"github.com/micoya/gocraft/cdao"
)

func init() {
	cdao.Register("database", factory)
}

func factory(_ string, raw any) (cdao.Provider, error) {
	cfg, ok := raw.(config.DBConfig)
	if !ok {
		return nil, fmt.Errorf("dao/provider/database: expected config.DBConfig, got %T", raw)
	}
	if cfg.Driver == "" {
		return nil, fmt.Errorf("dao/provider/database: driver is required")
	}
	if cfg.DSN == "" {
		return nil, fmt.Errorf("dao/provider/database: dsn is required")
	}
	return &provider{cfg: cfg}, nil
}

type provider struct {
	cfg config.DBConfig
	db  *gorm.DB
}

func (p *provider) Init(ctx context.Context) error {
	dialector, err := p.dialector()
	if err != nil {
		return err
	}

	p.db, err = gorm.Open(dialector, &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: p.cfg.DisableMigrateForeignKey == nil || *p.cfg.DisableMigrateForeignKey,
	})
	if err != nil {
		return err
	}

	sqlDB, err := p.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}

func (p *provider) dialector() (gorm.Dialector, error) {
	switch p.cfg.Driver {
	case "mysql":
		return gormmysql.Open(p.cfg.DSN), nil
	case "postgres":
		return gormpostgres.Open(p.cfg.DSN), nil
	default:
		return nil, fmt.Errorf("dao/provider/database: unsupported driver %q", p.cfg.Driver)
	}
}

func (p *provider) Close(_ context.Context) error {
	if p.db == nil {
		return nil
	}
	sqlDB, err := p.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func (p *provider) Health(ctx context.Context) error {
	sqlDB, err := p.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}

func (p *provider) Instance() any {
	return p.db
}

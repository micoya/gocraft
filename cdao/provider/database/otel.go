package database

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"gorm.io/gorm"
)

const gormTracerName = "cdao/database"

// gormOtelPlugin 为 GORM 注册 OTel trace 回调，追踪 SQL 执行耗时与错误。
// 覆盖 Create / Query / Update / Delete / Row / Raw 六类操作，
// 每次操作自动创建 client span 并记录 db.statement、db.system、db.sql.table 属性。
type gormOtelPlugin struct{}

func (gormOtelPlugin) Name() string { return "otel:tracing" }

func (gormOtelPlugin) Initialize(db *gorm.DB) error {
	cb := db.Callback()
	if err := cb.Create().Before("gorm:create").Register("otel:before_CREATE", beforeCallback("CREATE")); err != nil {
		return err
	}
	if err := cb.Create().After("gorm:after_create").Register("otel:after_CREATE", afterCallback); err != nil {
		return err
	}
	if err := cb.Query().Before("gorm:query").Register("otel:before_QUERY", beforeCallback("QUERY")); err != nil {
		return err
	}
	if err := cb.Query().After("gorm:after_query").Register("otel:after_QUERY", afterCallback); err != nil {
		return err
	}
	if err := cb.Update().Before("gorm:update").Register("otel:before_UPDATE", beforeCallback("UPDATE")); err != nil {
		return err
	}
	if err := cb.Update().After("gorm:after_update").Register("otel:after_UPDATE", afterCallback); err != nil {
		return err
	}
	if err := cb.Delete().Before("gorm:delete").Register("otel:before_DELETE", beforeCallback("DELETE")); err != nil {
		return err
	}
	if err := cb.Delete().After("gorm:after_delete").Register("otel:after_DELETE", afterCallback); err != nil {
		return err
	}
	if err := cb.Row().Before("gorm:row").Register("otel:before_ROW", beforeCallback("ROW")); err != nil {
		return err
	}
	if err := cb.Row().After("gorm:after_row").Register("otel:after_ROW", afterCallback); err != nil {
		return err
	}
	if err := cb.Raw().Before("gorm:raw").Register("otel:before_RAW", beforeCallback("RAW")); err != nil {
		return err
	}
	if err := cb.Raw().After("gorm:after_raw").Register("otel:after_RAW", afterCallback); err != nil {
		return err
	}
	return nil
}

// beforeCallback 在 SQL 执行前从 db.Statement.Context 启动 span。
func beforeCallback(op string) func(*gorm.DB) {
	return func(db *gorm.DB) {
		if db.Statement == nil || db.Statement.Context == nil {
			return
		}
		ctx, span := otel.Tracer(gormTracerName).Start(
			db.Statement.Context,
			"gorm "+op,
			trace.WithSpanKind(trace.SpanKindClient),
			trace.WithAttributes(attribute.String("db.operation", op)),
		)
		db.Statement.Context = ctx
		db.InstanceSet("_otel_span", span)
	}
}

// afterCallback 在 SQL 执行后结束 span，并附加 SQL 语句与错误信息。
func afterCallback(db *gorm.DB) {
	val, ok := db.InstanceGet("_otel_span")
	if !ok {
		return
	}
	span, ok := val.(trace.Span)
	if !ok {
		return
	}
	defer span.End()

	if db.Statement != nil {
		sql := db.Dialector.Explain(db.Statement.SQL.String(), db.Statement.Vars...)
		span.SetAttributes(
			attribute.String("db.statement", sql),
			attribute.String("db.system", db.Dialector.Name()),
		)
		if db.Statement.Table != "" {
			span.SetAttributes(attribute.String("db.sql.table", db.Statement.Table))
		}
	}
	if db.Error != nil && db.Error != gorm.ErrRecordNotFound {
		span.RecordError(db.Error)
		span.SetStatus(codes.Error, db.Error.Error())
	}
}

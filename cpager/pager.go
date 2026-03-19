// Package cpager 提供 offset-based 分页工具，包含参数归一化、GORM 集成和泛型结果类型。
//
// 基本用法：
//
//	page := cpager.New(c.Query("page"), c.Query("page_size"))
//
//	var result *cpager.Result[UserVO]
//	result, err = cpager.Paginate[UserVO](db.Model(&User{}).Where("status = ?", 1), page)
//
//	c.JSON(200, result)
package cpager

import (
	"fmt"
	"math"
	"strconv"

	"gorm.io/gorm"
)

const (
	defaultPage     = 1
	defaultPageSize = 20
	maxPageSize     = 100
)

// Page 表示一次分页请求的参数。
type Page struct {
	// page 从 1 开始
	page     int
	pageSize int
}

// New 根据原始字符串创建 Page，自动归一化无效值。
//
//	page := cpager.New(c.Query("page"), c.Query("page_size"))
func New(page, pageSize string) Page {
	p, _ := strconv.Atoi(page)
	ps, _ := strconv.Atoi(pageSize)
	return Of(p, ps)
}

// Of 根据整数值创建 Page，自动归一化无效值。
//
//	page := cpager.Of(1, 20)
func Of(page, pageSize int) Page {
	if page < 1 {
		page = defaultPage
	}
	if pageSize < 1 {
		pageSize = defaultPageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	return Page{page: page, pageSize: pageSize}
}

// Page 返回当前页码（从 1 开始）。
func (p Page) PageNum() int { return p.page }

// PageSize 返回每页条目数。
func (p Page) PageSize() int { return p.pageSize }

// Offset 返回 SQL OFFSET 值。
func (p Page) Offset() int { return (p.page - 1) * p.pageSize }

// Limit 返回 SQL LIMIT 值，等同于 PageSize。
func (p Page) Limit() int { return p.pageSize }

// Scope 返回一个 GORM 作用域函数，可直接传入 db.Scopes()。
//
//	db.Scopes(page.Scope).Find(&items)
func (p Page) Scope(db *gorm.DB) *gorm.DB {
	return db.Offset(p.Offset()).Limit(p.Limit())
}

// String 返回 "page=X,size=Y" 格式字符串，便于日志记录。
func (p Page) String() string {
	return fmt.Sprintf("page=%d,size=%d", p.page, p.pageSize)
}

// Result 是泛型分页结果。
type Result[T any] struct {
	Items      []T   `json:"items"`
	Total      int64 `json:"total"`
	Page       int   `json:"page"`
	PageSize   int   `json:"page_size"`
	TotalPages int   `json:"total_pages"`
	HasNext    bool  `json:"has_next"`
	HasPrev    bool  `json:"has_prev"`
}

// Paginate 对 GORM 查询执行分页，返回 Result[T]。
//
// db 应传入已链式拼接了 Where / Join / Order 等条件的查询：
//
//	result, err := cpager.Paginate[UserVO](
//	    db.Model(&User{}).Where("status = ?", 1).Order("created_at DESC"),
//	    page,
//	)
func Paginate[T any](db *gorm.DB, page Page) (*Result[T], error) {
	var total int64

	// 使用新 session 避免 Count 修改原有 query 的 select 子句
	if err := db.Session(&gorm.Session{NewDB: true}).Count(&total).Error; err != nil {
		return nil, fmt.Errorf("cpager: count: %w", err)
	}

	var items []T
	if err := db.Scopes(page.Scope).Find(&items).Error; err != nil {
		return nil, fmt.Errorf("cpager: find: %w", err)
	}

	totalPages := int(math.Ceil(float64(total) / float64(page.pageSize)))
	if totalPages < 1 {
		totalPages = 1
	}

	return &Result[T]{
		Items:      items,
		Total:      total,
		Page:       page.page,
		PageSize:   page.pageSize,
		TotalPages: totalPages,
		HasNext:    page.page < totalPages,
		HasPrev:    page.page > 1,
	}, nil
}

// Empty 返回一个空结果，用于条件分支提前返回。
func Empty[T any](page Page) *Result[T] {
	return &Result[T]{
		Items:      []T{},
		Total:      0,
		Page:       page.page,
		PageSize:   page.pageSize,
		TotalPages: 1,
		HasNext:    false,
		HasPrev:    false,
	}
}

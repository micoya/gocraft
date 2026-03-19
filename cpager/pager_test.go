package cpager

import (
	"testing"
)

// --- New() 参数归一化 ---

func TestNew_InvalidStrings(t *testing.T) {
	p := New("abc", "xyz")
	if p.PageNum() != defaultPage {
		t.Errorf("PageNum() = %d, want %d", p.PageNum(), defaultPage)
	}
	if p.PageSize() != defaultPageSize {
		t.Errorf("PageSize() = %d, want %d", p.PageSize(), defaultPageSize)
	}
}

func TestNew_EmptyStrings(t *testing.T) {
	p := New("", "")
	if p.PageNum() != defaultPage || p.PageSize() != defaultPageSize {
		t.Errorf("New(\"\",\"\") = page=%d size=%d, want %d %d",
			p.PageNum(), p.PageSize(), defaultPage, defaultPageSize)
	}
}

func TestNew_ValidStrings(t *testing.T) {
	p := New("3", "50")
	if p.PageNum() != 3 || p.PageSize() != 50 {
		t.Errorf("New(\"3\",\"50\") = page=%d size=%d, want 3 50", p.PageNum(), p.PageSize())
	}
}

// --- Of() 归一化规则 ---

func TestOf_PageLessThanOne(t *testing.T) {
	for _, v := range []int{0, -1, -99} {
		p := Of(v, 10)
		if p.PageNum() != 1 {
			t.Errorf("Of(%d, 10).PageNum() = %d, want 1", v, p.PageNum())
		}
	}
}

func TestOf_PageSizeLessThanOne(t *testing.T) {
	for _, v := range []int{0, -1, -99} {
		p := Of(1, v)
		if p.PageSize() != defaultPageSize {
			t.Errorf("Of(1, %d).PageSize() = %d, want %d", v, p.PageSize(), defaultPageSize)
		}
	}
}

func TestOf_PageSizeExceedsMax(t *testing.T) {
	p := Of(1, maxPageSize+1)
	if p.PageSize() != maxPageSize {
		t.Errorf("Of(1, %d).PageSize() = %d, want %d", maxPageSize+1, p.PageSize(), maxPageSize)
	}

	p2 := Of(1, 9999)
	if p2.PageSize() != maxPageSize {
		t.Errorf("Of(1, 9999).PageSize() = %d, want %d", p2.PageSize(), maxPageSize)
	}
}

func TestOf_Valid(t *testing.T) {
	p := Of(5, 25)
	if p.PageNum() != 5 || p.PageSize() != 25 {
		t.Errorf("Of(5, 25) = page=%d size=%d, want 5 25", p.PageNum(), p.PageSize())
	}
}

// --- Offset 计算 ---

func TestOffset(t *testing.T) {
	cases := []struct {
		page, size, wantOffset int
	}{
		{1, 20, 0},
		{2, 20, 20},
		{3, 10, 20},
		{5, 30, 120},
	}
	for _, c := range cases {
		p := Of(c.page, c.size)
		if got := p.Offset(); got != c.wantOffset {
			t.Errorf("Of(%d, %d).Offset() = %d, want %d", c.page, c.size, got, c.wantOffset)
		}
	}
}

func TestLimit(t *testing.T) {
	p := Of(3, 15)
	if p.Limit() != 15 {
		t.Errorf("Limit() = %d, want 15", p.Limit())
	}
}

// --- String ---

func TestString(t *testing.T) {
	p := Of(2, 30)
	got := p.String()
	want := "page=2,size=30"
	if got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

// --- Empty ---

func TestEmpty(t *testing.T) {
	p := Of(3, 20)
	r := Empty[string](p)

	if r.Total != 0 {
		t.Errorf("Total = %d, want 0", r.Total)
	}
	if r.TotalPages != 1 {
		t.Errorf("TotalPages = %d, want 1", r.TotalPages)
	}
	if r.HasNext || r.HasPrev {
		t.Errorf("HasNext=%v HasPrev=%v, want both false", r.HasNext, r.HasPrev)
	}
	if r.Page != 3 || r.PageSize != 20 {
		t.Errorf("Page=%d PageSize=%d, want 3 20", r.Page, r.PageSize)
	}
	if r.Items == nil {
		t.Error("Items should not be nil")
	}
	if len(r.Items) != 0 {
		t.Errorf("Items len = %d, want 0", len(r.Items))
	}
}

// --- HasNext / HasPrev 逻辑 ---

func TestHasNextHasPrev(t *testing.T) {
	// page=1, 共 3 页 → HasNext=true, HasPrev=false
	r1 := &Result[int]{Page: 1, PageSize: 10, Total: 25, TotalPages: 3, HasNext: true, HasPrev: false}
	if !r1.HasNext || r1.HasPrev {
		t.Errorf("page=1 of 3: HasNext=%v HasPrev=%v", r1.HasNext, r1.HasPrev)
	}

	// page=3, 共 3 页 → HasNext=false, HasPrev=true
	r3 := &Result[int]{Page: 3, PageSize: 10, Total: 25, TotalPages: 3, HasNext: false, HasPrev: true}
	if r3.HasNext || !r3.HasPrev {
		t.Errorf("page=3 of 3: HasNext=%v HasPrev=%v", r3.HasNext, r3.HasPrev)
	}
}

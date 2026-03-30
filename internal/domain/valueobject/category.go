package valueobject

import "fmt"

// Category 敏感词分类值对象
type Category string

const (
	CategoryPolitics Category = "politics"
	CategoryPorn     Category = "porn"
	CategoryAd       Category = "ad"
	CategoryViolence Category = "violence"
	CategoryAbuse    Category = "abuse"
	CategoryCustom   Category = "custom"
)

var validCategories = map[Category]bool{
	CategoryPolitics: true,
	CategoryPorn:     true,
	CategoryAd:       true,
	CategoryViolence: true,
	CategoryAbuse:    true,
	CategoryCustom:   true,
}

func (c Category) IsValid() bool {
	return validCategories[c]
}

func (c Category) String() string {
	return string(c)
}

func ParseCategory(s string) (Category, error) {
	c := Category(s)
	if !c.IsValid() {
		return "", fmt.Errorf("invalid category: %s", s)
	}
	return c, nil
}

func AllCategories() []Category {
	return []Category{
		CategoryPolitics, CategoryPorn, CategoryAd,
		CategoryViolence, CategoryAbuse, CategoryCustom,
	}
}

package valueobject

// WordStatus 词条状态值对象
type WordStatus int

const (
	WordStatusInactive WordStatus = 0
	WordStatusActive   WordStatus = 1
)

func (s WordStatus) IsValid() bool {
	return s == WordStatusInactive || s == WordStatusActive
}

func (s WordStatus) IsActive() bool {
	return s == WordStatusActive
}

func (s WordStatus) String() string {
	if s == WordStatusActive {
		return "active"
	}
	return "inactive"
}

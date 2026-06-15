package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExternalResourceFilters(t *testing.T) {
	tests := []struct {
		name       string
		cloudTypes []string
		want       []externalResourceFilter
	}{
		{
			name:       "video filter should use category=files type=video",
			cloudTypes: []string{"video"},
			want: []externalResourceFilter{
				{category: "files", typ: "video"},
			},
		},
		{
			name:       "default filters when empty",
			cloudTypes: []string{},
			want: []externalResourceFilter{
				{category: "cloud_drive"},
				{category: "magnet"},
				{category: "ed2k"},
				{category: "files", typ: "video"},
			},
		},
		{
			name:       "magnet filter",
			cloudTypes: []string{"magnet"},
			want: []externalResourceFilter{
				{category: "magnet"},
			},
		},
		{
			name:       "ed2k filter",
			cloudTypes: []string{"ed2k"},
			want: []externalResourceFilter{
				{category: "ed2k"},
			},
		},
		{
			name:       "cloud_drive filter",
			cloudTypes: []string{"cloud_drive"},
			want: []externalResourceFilter{
				{category: "cloud_drive"},
			},
		},
		{
			name:       "specific provider filter",
			cloudTypes: []string{"quark"},
			want: []externalResourceFilter{
				{category: "cloud_drive", typ: "quark"},
			},
		},
		{
			name:       "mixed filters",
			cloudTypes: []string{"video", "magnet", "quark"},
			want: []externalResourceFilter{
				{category: "files", typ: "video"},
				{category: "magnet"},
				{category: "cloud_drive", typ: "quark"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := externalResourceFilters(tt.cloudTypes)
			assert.Equal(t, tt.want, got)
		})
	}
}

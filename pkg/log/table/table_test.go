package table

import (
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	tests := []struct {
		name        string
		header      []string
		values      [][]string
		colorHeader bool
		want        []string
	}{
		{
			name:   "aligns columns",
			header: []string{"Name", "Provider", "last_used"},
			values: [][]string{{"my-workspace", "docker", "2d"}, {"x", "kubernetes", ""}},
			want: []string{
				"      NAME     |  PROVIDER  | LAST USED  ",
				"---------------+------------+------------",
				"  my-workspace | docker     | 2d         ",
				"  x            | kubernetes |            ",
			},
		},
		{
			name:   "wraps long cells",
			header: []string{"Name", "Description"},
			values: [][]string{
				{"opt", "This is a long description of an option that should wrap because it is longer than thirty characters"},
				{"url", "https://github.com/curityio/devpod-with-a-very-long-url-without-spaces"},
				{"multi", "line one\nline two"},
			},
			want: []string{
				"  NAME  |                              DESCRIPTION                                ",
				"--------+-------------------------------------------------------------------------",
				"  opt   | This is a long description of                                           ",
				"        | an option that should wrap                                              ",
				"        | because it is longer than                                               ",
				"        | thirty characters                                                       ",
				"  url   | https://github.com/curityio/devpod-with-a-very-long-url-without-spaces  ",
				"  multi | line one                                                                ",
				"        | line two                                                                ",
			},
		},
		{
			name:        "colors the header",
			header:      []string{"Name", "Value"},
			values:      [][]string{{"a", "1"}},
			colorHeader: true,
			want: []string{
				"  \x1b[32mNAME\x1b[0m | \x1b[32mVALUE\x1b[0m  ",
				"-------+--------",
				"  a    | 1      ",
			},
		},
		{
			name:   "no values",
			header: []string{"Name", "Value"},
			want: []string{
				"  NAME | VALUE  ",
				"-------+--------",
			},
		},
		{
			name:   "wide characters and numbers in the header",
			header: []string{"Näme", "Value", "v1.2"},
			values: [][]string{{"日本語", "ü", "x"}},
			want: []string{
				"   NÄME  | VALUE | V1.2  ",
				"---------+-------+-------",
				"  日本語 | ü     | x     ",
			},
		},
		{
			name:   "missing cells",
			header: []string{"A", "B"},
			values: [][]string{{"1"}},
			want: []string{
				"  A | B  ",
				"----+----",
				"  1 |    ",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := render(tt.header, tt.values, tt.colorHeader)
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Errorf("render() =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

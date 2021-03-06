//
// (C) Copyright 2020-2021 Intel Corporation.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// GOVERNMENT LICENSE RIGHTS-OPEN SOURCE SOFTWARE
// The Government's rights to use, modify, reproduce, release, perform, display,
// or disclose this software are subject to the terms of the Apache License as
// provided in Contract No. 8F-30005.
// Any reproduction of computer software, computer software documentation, or
// portions thereof marked with this legend must also reproduce the markings.
//

package txtfmt

import (
	"bytes"
	"fmt"
	"text/tabwriter"
)

const (
	defEntityRowIndent = 2
)

// EntityFormatter can be used for neatly displaying attributes
// of a single entity.
type EntityFormatter struct {
	title  string
	writer *tabwriter.Writer
	out    bytes.Buffer

	Separator string
}

func (f *EntityFormatter) formatHeader() {
	if f.title == "" {
		return
	}

	fmt.Fprintf(&f.out, "%s\n", f.title)
	for i := 0; i < len(f.title); i++ {
		fmt.Fprintf(&f.out, "-")
	}
	fmt.Fprintf(&f.out, "\n")
}

// Format generates an output string for the supplied table rows.
// It includes a single subject header, and each row is printed as an
// attribute/value pair.
func (f *EntityFormatter) Format(table []TableRow) string {
	f.formatHeader()

	iw := NewIndentWriter(f.writer, WithPadCount(defEntityRowIndent))
	for _, row := range table {
		for key, val := range row {
			fmt.Fprintf(iw, "%s\t%s%s\t\n", key, f.Separator, val)
		}
	}

	f.writer.Flush()
	return f.out.String()
}

// Init instantiates internal variables.
func (f *EntityFormatter) Init(padWidth int) {
	f.writer = tabwriter.NewWriter(&f.out, padWidth, 0, 0, ' ', 0)
}

// NewEntityFormatter returns an initialized EntityFormatter.
func NewEntityFormatter(title string, padWidth int) *EntityFormatter {
	f := &EntityFormatter{
		title:     title,
		Separator: ": ",
	}
	f.Init(padWidth)
	return f
}

// GetEntityPadding will determine the minimum width necessary
// to display the entity attributes.
func GetEntityPadding(table []TableRow) (padding int) {
	for _, row := range table {
		for key := range row {
			if len(key) > padding {
				padding = len(key) + 1
			}
		}
	}

	return
}

// FormatEntity returns a formatted string from the supplied entity title
// and table of attributes.
func FormatEntity(title string, attrs []TableRow) string {
	f := NewEntityFormatter(title, GetEntityPadding(attrs)+defEntityRowIndent)
	return f.Format(attrs)
}

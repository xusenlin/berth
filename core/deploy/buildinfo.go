package deploy

import (
	"bytes"
	"debug/buildinfo"
	"io"
	"os"
	"regexp"
	"runtime/debug"
	"strings"

	"github.com/xusenlin/berth/core/app"
)

// maxBuildRead is the largest entry of an archive read into memory for its
// build info.
const maxBuildRead = 1 << 30

// pseudoVersion matches the versions Go gives untagged commits, as
// v0.0.0-20261009085455-01b4f77867b6.
var pseudoVersion = regexp.MustCompile(`-(0\.)?\d{14}-[0-9a-f]{12}$`)

// readBuild returns the build info of the entry when it is a Go program,
// nil otherwise.
func (p *Package) readBuild(e *entry) *app.Build {
	var r io.ReaderAt
	switch {
	case e.data != nil:
		r = bytes.NewReader(e.data)
	case p.Kind == KindBinary:
		f, err := os.Open(p.Path)
		if err != nil {
			return nil
		}
		defer f.Close()
		r = f
	case e.open != nil && e.size <= maxBuildRead:
		rc, err := e.open()
		if err != nil {
			return nil
		}
		data, err := io.ReadAll(io.LimitReader(rc, maxBuildRead))
		rc.Close()
		if err != nil {
			return nil
		}
		r = bytes.NewReader(data)
	default:
		return nil
	}
	bi, err := buildinfo.Read(r)
	if err != nil {
		return nil
	}
	return buildOf(bi)
}

func buildOf(bi *debug.BuildInfo) *app.Build {
	b := &app.Build{}
	v := strings.TrimSuffix(bi.Main.Version, "+dirty")
	if v != "(devel)" && !pseudoVersion.MatchString(v) {
		b.Version = v
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			b.Revision = s.Value
		case "vcs.time":
			b.Time = s.Value
		case "vcs.modified":
			b.Modified = s.Value == "true"
		}
	}
	if *b == (app.Build{}) {
		return nil
	}
	return b
}

// BuildLabel describes a build in a few words, as v1.0.1 (01b4f77), or ""
// for nil.
func BuildLabel(b *app.Build) string {
	if b == nil {
		return ""
	}
	s := b.Version
	if rev := b.Revision[:min(len(b.Revision), 7)]; rev != "" {
		if s != "" {
			rev = " (" + rev + ")"
		}
		s += rev
	}
	if b.Modified {
		s += "，有未提交改动"
	}
	return strings.TrimPrefix(s, "，")
}

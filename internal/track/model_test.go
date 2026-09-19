package track

import (
	"testing"
	"time"
)

func TestSliceKey(t *testing.T) {
	base := Slice{
		SourceAudioPath: "/music/album.flac",
		SourceModTime:   time.Unix(1700000000, 0),
		SourceSize:      123456,
		Start:           0,
		End:             60,
		Tags: map[string]string{
			"title": "Song Title",
			"track": "1",
		},
	}
	original := base.Key()

	// 1. Deterministic
	identical := base
	if identical.Key() != original {
		t.Error("key must be deterministic")
	}

	// 2. TargetSampleRate changes key
	resampled := base
	resampled.TargetSampleRate = 96000
	if resampled.Key() == original {
		t.Error("TargetSampleRate must change the cache key")
	}

	// 3. TargetBits changes key
	redepthed := base
	redepthed.TargetBits = 16
	if redepthed.Key() == original || redepthed.Key() == resampled.Key() {
		t.Error("TargetBits must change the cache key independently")
	}

	// 4. Tags change key
	withDifferentTag := base
	withDifferentTag.Tags = map[string]string{"title": "Different"}
	if withDifferentTag.Key() == original {
		t.Error("different tags must change key")
	}
}

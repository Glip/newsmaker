package media

import "testing"

func TestParseFFMPEGProbe(t *testing.T) {
	sample := `Input #0, mov,mp4,m4a,3gp,3g2,mj2, from 'clip.mp4':
  Duration: 00:01:30.50, start: 0.000000, bitrate: 1600 kb/s
  Stream #0:0: Video: h264
  Stream #0:1(und): Audio: aac (LC)`
	got, err := parseFFMPEGProbe(sample)
	if err != nil {
		t.Fatal(err)
	}
	if got.Duration < 90.49 || got.Duration > 90.51 {
		t.Fatalf("duration = %v", got.Duration)
	}
	if !got.HasAudio {
		t.Fatal("expected audio")
	}
}

func TestFitVideoBitrate18MB(t *testing.T) {
	const size = 18 << 20
	// A minute-long clip can be squeezed under Discord 8 MB and Telegram 10 MB.
	for _, platform := range []string{"discord", "telegram"} {
		lim := DefaultLimits(platform)
		bps, ok := fitVideoBitrate(lim.MaxBytes, 60, videoAudioBitrate)
		if !ok {
			t.Fatalf("%s: expected a minute-long video to fit, bps=%d", platform, bps)
		}
		bits := float64(bps+videoAudioBitrate) * 60
		if bits/8 > float64(lim.MaxBytes) {
			t.Fatalf("%s: bitrate budget exceeds cap", platform)
		}
	}
	report := VideoLimitReport(size, 60)
	if len(report) < 2 {
		t.Fatalf("report = %+v", report)
	}
	by := map[string]VideoLimit{}
	for _, row := range report {
		by[row.Platform] = row
	}
	if !by["discord"].Over || !by["discord"].CanFit {
		t.Fatalf("discord = %+v", by["discord"])
	}
	if !by["telegram"].Over || !by["telegram"].CanFit {
		t.Fatalf("telegram = %+v", by["telegram"])
	}
	if _, vk := by["vk"]; vk {
		t.Fatal("18 MB is under the VK cap")
	}

	// Ten minutes cannot fit 8 MB at a watchable bitrate.
	long := VideoLimitReport(size, 600)
	for _, row := range long {
		if row.Platform == "discord" && row.CanFit {
			t.Fatalf("discord should not fit a 10 minute file into 8 MB: %+v", row)
		}
	}
}

func TestVideoWidthStepsDown(t *testing.T) {
	if w := videoWidth(1280, 2_000_000); w != 1280 {
		t.Fatalf("high bitrate width = %d", w)
	}
	if w := videoWidth(1280, 200_000); w != 480 {
		t.Fatalf("low bitrate width = %d", w)
	}
}

func TestVideoLimitError(t *testing.T) {
	err := videoLimitError("discord", 8<<20)
	if err == nil || err.Error() != "Discord: видео больше лимита 8 МБ даже после сжатия. Укоротите ролик" {
		t.Fatal(err)
	}
}

package publish

import (
	"context"
	"fmt"

	"newsmaker/internal/channels"
	"newsmaker/internal/media"
)

type Post struct {
	TextHTML string
	Media    []media.Asset
}

type Result struct {
	ChannelID   int64  `json:"channel_id"`
	Platform    string `json:"platform"`
	ChannelName string `json:"channel_name"`
	OK          bool   `json:"ok"`
	MessageRef  string `json:"message_ref"`
	PostURL     string `json:"post_url"`
	Error       string `json:"error"`
}

type Publisher interface {
	Platform() channels.Platform
	Publish(ctx context.Context, ch channels.Channel, post Post, prepared []string) (string, error)
}

type Dispatcher struct {
	Publishers map[channels.Platform]Publisher
	Media      *media.Processor
}

func (d *Dispatcher) Send(ctx context.Context, ch channels.Channel, post Post) Result {
	res := Result{
		ChannelID:   ch.ID,
		Platform:    string(ch.Platform),
		ChannelName: ch.Name,
	}
	pub, ok := d.Publishers[ch.Platform]
	if !ok {
		res.Error = fmt.Sprintf("no publisher for %s", ch.Platform)
		return res
	}
	prepared := make([]string, 0, len(post.Media))
	for _, a := range post.Media {
		var path string
		var err error
		switch a.Kind {
		case media.KindPhoto:
			path, err = d.Media.PreparePhoto(a, string(ch.Platform))
		case media.KindVideo:
			path, err = d.Media.PrepareVideo(a, string(ch.Platform))
		case media.KindAudio:
			path, err = d.Media.PrepareAudio(a, string(ch.Platform))
		default:
			err = fmt.Errorf("unknown media kind")
		}
		if err != nil {
			res.Error = err.Error()
			return res
		}
		prepared = append(prepared, path)
	}
	ref, err := pub.Publish(ctx, ch, post, prepared)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.OK = true
	res.MessageRef = ref
	res.PostURL = BuildPostURL(ch.Platform, ch.TargetID, ch.Credential, ref)
	return res
}

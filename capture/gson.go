package capture

import (
	"github.com/go-rod/rod/lib/proto"
	"github.com/ysmood/gson"
)

func gsonString(s string) gson.JSON { return gson.New(s) }

func protoBlank() proto.TargetCreateTarget { return proto.TargetCreateTarget{URL: "about:blank"} }

package toolbox

import (
	"strings"
	"testing"
)

// youtubeAutoVTT is a trimmed YouTube auto caption file (yt-dlp, en-orig): word
// timestamps, <c> tags, a line holding one space inside each cue ({sp} in the
// source, so editors keep it) and rolling cues that repeat the previous cue's
// last line.
var youtubeAutoVTT = strings.ReplaceAll(`WEBVTT
Kind: captions
Language: en

00:00:00.320 --> 00:00:18.790 align:start position:0%
{sp}
[Music]

00:00:18.790 --> 00:00:18.800 align:start position:0%
{sp}
{sp}

00:00:18.800 --> 00:00:21.790 align:start position:0%
{sp}
We're<00:00:19.039><c> no</c><00:00:19.359><c> strangers</c><00:00:19.840><c> to</c>

00:00:21.790 --> 00:00:21.800 align:start position:0%
We're no strangers to
{sp}

00:00:21.800 --> 00:00:25.950 align:start position:0%
We're no strangers to
love.<00:00:22.800><c> You</c><00:00:23.039><c> know</c><00:00:23.279><c> the</c><00:00:23.600><c> rules</c><00:00:24.320><c> and</c><00:00:24.640><c> so</c><00:00:25.199><c> do</c>

00:00:25.950 --> 00:00:25.960 align:start position:0%
love. You know the rules and so do
{sp}

00:00:25.960 --> 00:00:29.109 align:start position:0%
love. You know the rules and so do
I.<00:00:27.039><c> I</c><00:00:27.279><c> feel</c><00:00:27.680><c> commitments</c><00:00:28.400><c> from</c><00:00:28.400><c> what</c><00:00:28.640><c> I'm</c>

00:00:29.109 --> 00:00:29.119 align:start position:0%
I. I feel commitments from what I'm
{sp}

00:00:29.119 --> 00:00:30.269 align:start position:0%
I. I feel commitments from what I'm
thinking

00:00:30.269 --> 00:00:30.279 align:start position:0%
thinking
{sp}

00:00:30.279 --> 00:00:34.350 align:start position:0%
thinking
of.<00:00:31.279><c> You</c><00:00:31.599><c> wouldn't</c><00:00:32.079><c> get</c><00:00:32.320><c> this</c><00:00:32.480><c> from</c><00:00:33.200><c> any</c><00:00:33.680><c> other</c>

00:00:34.350 --> 00:00:34.360 align:start position:0%
of. You wouldn't get this from any other
{sp}

00:00:34.360 --> 00:00:39.549 align:start position:0%
of. You wouldn't get this from any other
guy.<00:00:35.480><c> I</c><00:00:36.480><c> just</c><00:00:36.800><c> want</c><00:00:37.040><c> to</c><00:00:37.280><c> tell</c><00:00:37.520><c> you</c><00:00:37.840><c> how</c><00:00:38.160><c> I'm</c>
`, "{sp}", " ")

// TestSubtitlesToText turns WebVTT and SRT documents into plain transcript
// text (REQ-TB-012).
func TestSubtitlesToText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "youtube auto captions with rolling cues",
			in:   youtubeAutoVTT,
			want: "[Music] We're no strangers to love. You know the rules and so do I. I feel commitments from what I'm thinking of. You wouldn't get this from any other guy. I just want to tell you how I'm",
		},
		{
			name: "manual vtt with header, note, style, cue ids and entities",
			in: "\ufeffWEBVTT - Title\r\nKind: captions\r\n\r\nSTYLE\r\n::cue { color: red }\r\n\r\nNOTE a comment\r\nthat spans lines\r\n\r\n" +
				"intro\r\n00:00:01.000 --> 00:00:02.000\r\n<v Roger>Tom &amp; Jerry &gt; cats</v>\r\n\r\n" +
				"2\r\n00:00:02.500 --> 00:00:04.000 line:0\r\n<i>It&#39;s &quot;fine&quot;</i>&nbsp;now\r\n",
			want: "Tom & Jerry > cats It's \"fine\" now",
		},
		{
			name: "srt with numbers and ass tags",
			in:   "1\n00:00:01,000 --> 00:00:02,000\n{\\an8}Hello there.\n\n2\n00:00:02,000 --> 00:00:03,000\nGeneral Kenobi!\n",
			want: "Hello there. General Kenobi!",
		},
		{
			name: "consecutive duplicate lines collapse",
			in:   "WEBVTT\n\n00:01.000 --> 00:02.000\n♪ la la ♪\n\n00:02.000 --> 00:03.000\n♪ la la ♪\n\n00:03.000 --> 00:04.000\nend\n",
			want: "♪ la la ♪ end",
		},
		{
			name: "two-line cue repeated in the next cue",
			in:   "WEBVTT\n\n00:01.000 --> 00:02.000\nfirst line\nsecond line\n\n00:02.000 --> 00:03.000\nfirst line\nsecond line\nthird line\n",
			want: "first line second line third line",
		},
		{
			name: "long text breaks after a sentence",
			in: "WEBVTT\n\n00:01.000 --> 00:02.000\nThis is a long first sentence that keeps going for quite a while here.\n\n" +
				"00:02.000 --> 00:03.000\nIt ends now.\n\n00:03.000 --> 00:04.000\nNext one.\n",
			want: "This is a long first sentence that keeps going for quite a while here. It ends now.\nNext one.",
		},
		{
			name: "text without cues is returned trimmed",
			in:   "  just some text\n",
			want: "just some text",
		},
		{
			name: "header only",
			in:   "WEBVTT\n\n00:01.000 --> 00:02.000\n \n",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SubtitlesToText(tc.in); got != tc.want {
				t.Fatalf("got:\n%q\nwant:\n%q", got, tc.want)
			}
		})
	}
}

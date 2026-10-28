package analyze

import (
	"strings"

	"github.com/piplon-sh/piplon/internal/probe"
)

// Probable causes of the learned states.
const (
	causeSlow      = "slower than usual at this time: the provider or the path to it is degraded"
	causeCacheMiss = "the prompt cache is no longer read: the provider stopped caching, or a hop changes the prompt"
)

// cause is the probable cause of a failed turn, one sentence an operator can
// act on. s is the state the turn was judged.
func cause(s State, t probe.Turn) string {
	switch {
	case s == Gone:
		return "the model is not found: retired, renamed, or not enabled for this key or region"
	case s == Refused:
		if category, ok := strings.CutPrefix(t.Error, "refused: "); ok {
			return "the model declined the scenario (category " + category + ")"
		}
		return "the model declined the scenario"
	case t.Error == "":
		return "the answer did not have the expected shape"
	case t.Error == "timeout":
		return "no complete answer within the timeout: the provider or the network is slow"
	case t.Error == "tool call instead of text":
		return "the model called a tool instead of answering"
	case t.StreamCut:
		return "the stream was cut before the end of the answer"
	case t.HTTPStatus == 401 || t.HTTPStatus == 403:
		return "the key was rejected: check its value, its permissions and the account's billing"
	case t.HTTPStatus == 429:
		return "rate limit or quota reached for this key"
	case t.HTTPStatus == 529 || t.ProviderError == "overloaded_error":
		return "the provider is overloaded"
	case t.HTTPStatus == 400 || t.HTTPStatus == 422:
		return "the request was rejected: the model name or a parameter is not accepted"
	case t.HTTPStatus >= 500:
		return "the provider or a gateway returned a server error"
	case t.HTTPStatus == 0:
		return "no HTTP answer: DNS, TLS or the network between this host and the provider"
	}
	return "unexpected answer from the provider"
}

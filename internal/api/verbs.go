package api

import (
	"context"
	"strings"
	"time"
)

// need is the set of optional dependencies a verb cannot run without.
type need uint8

const (
	needsReceipts need = 1 << iota
	needsStores
)

type verb struct {
	Name     string
	Aliases  []string
	Needs    need
	Audience Audience

	// Usage is the line help prints and the suggestion a parse error carries.
	Usage string

	// Parse reports false when args are not this verb's arguments at all, which
	// makes the message ordinary chat. A matched verb carrying a wrong value
	// says so in Command.Err instead.
	Parse func(args string, now time.Time) (Command, bool)
	Run   func(*Server, context.Context, request) string
}

// verbs is the whole command set. Run returns a string rather than a Reply so
// that an executor cannot contradict the Audience its own row declares.
var verbs = []verb{
	{Name: "help", Aliases: []string{"?", "commands"}, Audience: audienceSender,
		Usage: "help, what I understand",
		Parse: noArgs, Run: (*Server).helpReply},

	{Name: "who", Aliases: []string{"relay"}, Audience: audienceSender,
		Usage: "who, who is on the relay",
		Parse: noArgs, Run: (*Server).whoReply},

	{Name: "stores", Aliases: []string{"shops", "merchants"}, Needs: needsStores,
		Audience: audienceSender,
		Usage:    "stores, shops I have learned",
		Parse:    noArgs, Run: (*Server).storesReply},

	{Name: "last", Aliases: []string{"recent"}, Needs: needsReceipts, Audience: audienceSender,
		Usage: "last, last 5",
		Parse: parseLast, Run: (*Server).lastReply},

	{Name: "total", Aliases: []string{"sum", "spent"}, Needs: needsReceipts,
		Audience: audienceSender,
		Usage:    "total, total last month, total 2026-08, total all, total ever",
		Parse:    parseTotal, Run: (*Server).totalReply},

	{Name: "add", Needs: needsReceipts, Audience: audienceEveryone,
		Usage: "add 150 ICA, a receipt with no photo",
		Parse: parseAdd, Run: (*Server).addReply},

	{Name: "edit", Needs: needsReceipts, Audience: audienceEveryone,
		Usage: "edit 7 merchant: ICA, or edit total: 154.53 for the newest",
		Parse: parseEdit, Run: (*Server).editReply},

	// audienceEveryone because the echo of what vanished is the only backup a
	// deleted receipt gets, and the chat log is where it survives.
	{Name: "delete", Aliases: []string{"remove", "rm"}, Needs: needsReceipts,
		Audience: audienceEveryone,
		Usage:    "delete 7",
		Parse:    parseDelete, Run: (*Server).deleteReply},
}

var verbIndex = indexVerbs()

func indexVerbs() map[string]*verb {
	index := make(map[string]*verb, len(verbs)*2)
	for i := range verbs {
		v := &verbs[i]
		index[v.Name] = v
		for _, alias := range v.Aliases {
			index[alias] = v
		}
	}
	return index
}

const helpPreamble = `*Receipt bot*

Send a photo of a receipt and I'll read it.

Commands:`

const helpEpilogue = `
Fields: merchant, total, subtotal, tax, date

Correcting a merchant teaches me that shop, so the next receipt from the same
company gets the name automatically.`

var helpText string

// helpText is assembled here instead of in its own var initializer because the
// verb table refers to helpReply, which reads helpText, and Go rejects that as
// an initialization cycle.
func init() { helpText = buildHelpText() }

func buildHelpText() string {
	var b strings.Builder

	b.WriteString(helpPreamble)
	for _, v := range verbs {
		b.WriteString("\n")
		b.WriteString(v.Usage)
	}
	b.WriteString("\n")
	b.WriteString(helpEpilogue)

	return b.String()
}

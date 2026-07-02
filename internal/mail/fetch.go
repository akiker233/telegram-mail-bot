package mail

import (
	"bytes"
	"fmt"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

// FetchNewSummaries 拉取 UID 大于 lastUID 的邮件，返回摘要列表和本次拉取到的最大 UID。
// 若没有新邮件，返回空列表和原 lastUID。
func FetchNewSummaries(c *client.Client, lastUID uint32) ([]*Summary, uint32, error) {
	seqSet := new(imap.SeqSet)
	seqSet.AddRange(lastUID+1, 0) // 0 表示 "*"，即到最新一封

	section := &imap.BodySectionName{}
	fetchItems := []imap.FetchItem{imap.FetchUid, section.FetchItem()}

	messages := make(chan *imap.Message, 16)
	done := make(chan error, 1)
	go func() {
		done <- c.UidFetch(seqSet, fetchItems, messages)
	}()

	var summaries []*Summary
	maxUID := lastUID
	for msg := range messages {
		if msg.Uid <= lastUID {
			continue
		}
		literal := msg.GetBody(section)
		if literal == nil {
			continue
		}
		buf := new(bytes.Buffer)
		if _, err := buf.ReadFrom(literal); err != nil {
			continue
		}
		summary, err := BuildSummary(bytes.NewReader(buf.Bytes()))
		if err != nil {
			continue
		}
		summaries = append(summaries, summary)
		if msg.Uid > maxUID {
			maxUID = msg.Uid
		}
	}

	if err := <-done; err != nil {
		return nil, lastUID, fmt.Errorf("mail: uid fetch: %w", err)
	}

	return summaries, maxUID, nil
}

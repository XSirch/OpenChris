package research

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"
	"time"
)

type feedItem struct {
	Title       feedText `xml:"title"`
	Link        string   `xml:"link"`
	Description feedText `xml:"description"`
	Content     feedText `xml:"encoded"`
	Published   string   `xml:"pubDate"`
	Date        string   `xml:"date"`
}

type feedText struct {
	Text  string `xml:",chardata"`
	Inner string `xml:",innerxml"`
}

func (text feedText) value() string {
	if strings.TrimSpace(text.Text) != "" {
		return text.Text
	}
	return text.Inner
}

type atomEntry struct {
	Title feedText `xml:"title"`
	Links []struct {
		Href string `xml:"href,attr"`
		Rel  string `xml:"rel,attr"`
	} `xml:"link"`
	Summary   feedText `xml:"summary"`
	Content   feedText `xml:"content"`
	Published string   `xml:"published"`
}

func (c *Client) parseFeed(body []byte, source Source, baseURL string, collected time.Time) ([]Document, error) {
	var feed struct {
		XMLName xml.Name
		Channel *struct {
			Items []feedItem `xml:"item"`
		} `xml:"channel"`
		Entries []atomEntry `xml:"entry"`
	}
	decoder := xml.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&feed); err != nil {
		return nil, err
	}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch tail := token.(type) {
		case xml.Comment, xml.ProcInst:
		case xml.CharData:
			if strings.TrimSpace(string(tail)) != "" {
				return nil, errors.New("invalid content after feed root")
			}
		default:
			return nil, errors.New("invalid content after feed root")
		}
	}
	var items []feedItem
	if feed.Channel != nil {
		items = feed.Channel.Items
	}
	if feed.XMLName.Local == "feed" {
		for _, entry := range feed.Entries {
			link := ""
			for _, candidate := range entry.Links {
				if candidate.Rel == "" || candidate.Rel == "alternate" {
					link = candidate.Href
					break
				}
			}
			summary := entry.Summary
			if summary.value() == "" {
				summary = entry.Content
			}
			items = append(items, feedItem{Title: entry.Title, Link: link, Description: summary, Published: entry.Published})
		}
	} else if feed.XMLName.Local != "rss" || feed.Channel == nil {
		return nil, errors.New("response is not an RSS or Atom feed")
	}
	documents := make([]Document, 0, 30)
	seenURL, seenHash := make(map[string]bool), make(map[string]bool)
	for _, item := range items {
		summary := item.Description.value()
		if summary == "" {
			summary = item.Content.value()
		}
		published := parseDate(item.Published, item.Date)
		title := plainText(item.Title.value(), 300)
		summary = plainText(summary, 600)
		if strings.TrimSpace(item.Link) == "" || title == "" && summary == "" {
			return nil, ErrEvidenceUnavailable
		}
		link, err := c.canonicalURL(cvmLegacyHTTPS(item.Link, source.URL), baseURL)
		if err != nil {
			return nil, err
		}
		hash := contentHash(title, summary, published)
		if seenURL[link] || seenHash[hash] {
			continue
		}
		seenURL[link], seenHash[hash] = true, true
		documents = append(documents, Document{URL: link, Title: title, Summary: summary, Kind: source.Kind, PublishedAt: published, CollectedAt: collected, Hash: hash})
		if len(documents) == 30 {
			break
		}
	}
	return documents, nil
}

func parseDate(values ...string) *time.Time {
	for _, value := range values {
		for _, layout := range []string{time.RFC3339Nano, time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822, "2006-01-02"} {
			if date, err := time.Parse(layout, strings.TrimSpace(value)); err == nil {
				return &date
			}
		}
	}
	return nil
}

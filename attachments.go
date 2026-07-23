package main

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"os"
	"path/filepath"
	"strings"

	mbox "github.com/emersion/go-mbox"
)

// Attachments.
//
// For a mail store feeding document workflows, the attachment often *is* the
// answer — a bill, a receipt, a ticket. tb could find the message carrying it
// and then had no way to say what was attached, let alone get it out.

type attachmentInfo struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type,omitempty"`
	Bytes       int    `json:"bytes,omitempty"`
}

// listAttachments walks a message's MIME tree and reports its attached parts.
// Inline text parts that make up the body are not attachments and are skipped.
func listAttachments(h mail.Header, body []byte) []attachmentInfo {
	var out []attachmentInfo
	walkParts(h, body, func(partHeader mail.Header, partBody []byte) {
		name := attachmentName(partHeader)
		if name == "" {
			return
		}
		mediaType, _, _ := mime.ParseMediaType(partHeader.Get("Content-Type"))
		decoded, err := decodeBodyContent(partHeader.Get("Content-Transfer-Encoding"), partBody)
		size := len(partBody)
		if err == nil {
			size = len(decoded)
		}
		out = append(out, attachmentInfo{Filename: name, ContentType: mediaType, Bytes: size})
	})
	return out
}

// saveAttachments writes a message's attachments into dir and returns what it
// wrote. Filenames from mail are untrusted, so each is reduced to its base name
// and any path traversal is discarded.
func saveAttachments(h mail.Header, body []byte, dir string) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var written []string
	var firstErr error
	walkParts(h, body, func(partHeader mail.Header, partBody []byte) {
		name := attachmentName(partHeader)
		if name == "" {
			return
		}
		safe := safeAttachmentName(name)
		if safe == "" {
			return
		}
		decoded, err := decodeBodyContent(partHeader.Get("Content-Transfer-Encoding"), partBody)
		if err != nil {
			decoded = partBody
		}
		path := uniquePath(filepath.Join(dir, safe))
		if err := os.WriteFile(path, decoded, 0o644); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return
		}
		written = append(written, path)
	})
	return written, firstErr
}

// walkParts visits every leaf part of a MIME message.
func walkParts(h mail.Header, body []byte, visit func(mail.Header, []byte)) {
	mediaType, params, err := mime.ParseMediaType(h.Get("Content-Type"))
	if err != nil || mediaType == "" {
		visit(h, body)
		return
	}
	if !strings.HasPrefix(mediaType, "multipart/") {
		visit(h, body)
		return
	}
	boundary := params["boundary"]
	if boundary == "" {
		return
	}
	mr := multipart.NewReader(bytes.NewReader(body), boundary)
	for {
		part, err := mr.NextPart()
		if err != nil {
			return
		}
		partBody, _ := io.ReadAll(io.LimitReader(part, maxPartBytes))
		walkParts(mail.Header(part.Header), partBody, visit)
	}
}

// attachmentName returns the declared filename of an attached part, or "" when
// the part is body content rather than an attachment.
func attachmentName(h mail.Header) string {
	decoder := new(mime.WordDecoder)
	disposition, dParams, _ := mime.ParseMediaType(h.Get("Content-Disposition"))
	name := dParams["filename"]
	if name == "" {
		if _, cParams, err := mime.ParseMediaType(h.Get("Content-Type")); err == nil {
			name = cParams["name"]
		}
	}
	if name == "" {
		return ""
	}
	// A named inline part that is not marked as an attachment is usually an
	// embedded image belonging to the body; only count it when disposition says
	// attachment, or when there is no disposition at all.
	if disposition != "" && !strings.EqualFold(disposition, "attachment") {
		return ""
	}
	if decoded, err := decoder.DecodeHeader(name); err == nil && decoded != "" {
		name = decoded
	}
	return strings.TrimSpace(name)
}

// safeAttachmentName strips directory components and rejects traversal.
func safeAttachmentName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(filepath.Clean("/" + name))
	name = strings.TrimSpace(name)
	switch name {
	case "", ".", "/", "..":
		return ""
	}
	return name
}

// uniquePath avoids clobbering an existing file when two messages carry the
// same attachment name.
func uniquePath(path string) string {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path
	}
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	for i := 2; i < 1000; i++ {
		candidate := fmt.Sprintf("%s (%d)%s", base, i, ext)
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
	return path
}

// saveMessageAttachments locates one message and writes its attachments to disk.
func (a *App) saveMessageAttachments(profileName, messageID, folderLike, query, accountEmail, dir string) error {
	profile, err := a.resolveProfile(profileName)
	if err != nil {
		return err
	}
	if strings.TrimSpace(messageID) == "" {
		return fmt.Errorf("--save-attachments needs --message-id (get it from `tb q`)")
	}
	target, err := a.findByMessageID(profile, messageID)
	if err != nil {
		return err
	}
	boxes, err := a.listMailboxes(profile)
	if err != nil {
		return err
	}
	for _, box := range boxes {
		if box.Name != target.Folder {
			continue
		}
		f, err := os.Open(box.Path)
		if err != nil {
			return err
		}
		defer f.Close()
		reader := mbox.NewReader(f)
		for {
			msgReader, err := reader.NextMessage()
			if err != nil {
				break
			}
			raw, err := io.ReadAll(io.LimitReader(msgReader, maxMessageBytes))
			if err != nil {
				continue
			}
			parsed, err := mail.ReadMessage(bytes.NewReader(raw))
			if err != nil {
				continue
			}
			if strings.TrimSpace(parsed.Header.Get("Message-Id")) != strings.TrimSpace(messageID) {
				continue
			}
			body, _ := io.ReadAll(io.LimitReader(parsed.Body, maxMessageBytes))
			written, err := saveAttachments(parsed.Header, body, dir)
			if err != nil {
				return err
			}
			if len(written) == 0 {
				fmt.Println("No attachments on that message.")
				return nil
			}
			for _, p := range written {
				fmt.Println(p)
			}
			return nil
		}
	}
	return fmt.Errorf("could not re-read %s from %s", messageID, target.Folder)
}

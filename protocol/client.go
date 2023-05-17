package protocol

import (
	"errors"
	"io"
	"net"
	"time"

	"github.com/gobwas/ws"
)

type ClientMessage struct {
	Type MessageType // 1 byte
	// reserved1 byte        // 1 byte
	ApplicationID AppID
	Headers       []byte // uint16 len
	Body          []byte // uint32 len
	Timestamp     int64  // так как в ответах от сервера всегда будет, то пихать его в хедеры странно
}

const Client_message_head_len = 18

func (m *ClientMessage) ReadWS(r io.Reader, h ws.Header) error {
	if h.Length < Client_message_head_len {
		if h.Length == 9 { // timestamp message (9 bytes)
			payload := make([]byte, h.Length)
			_, err := io.ReadFull(r, payload)
			if err != nil {
				return err
			}
			if payload[0] == byte(TypeTimestamp) {
				m.Type = TypeTimestamp
				m.Timestamp = int64(byteOrder.Uint64(payload[1:9]))
				return nil
			}
		}
		return errors.New("ws payload is less than client message head len")
	}
	payload := make([]byte, h.Length)
	_, err := io.ReadFull(r, payload)
	if err != nil {
		return err
	}
	headers_len := byteOrder.Uint16(payload[12:14])
	body_len := byteOrder.Uint32(payload[14:18])
	if h.Length != int64(Client_message_head_len)+int64(headers_len)+int64(body_len) {
		return errors.New("wsheader.length does not match with specified len in protocol's message head")
	}
	m.Headers = payload[18 : 18+headers_len]
	m.Body = payload[18+headers_len:]

	m.Type = MessageType(payload[0])
	m.ApplicationID = AppID(byteOrder.Uint16(payload[2:4]))
	m.Timestamp = int64(byteOrder.Uint64(payload[4:12]))
	return nil

}

func (m *ClientMessage) Read(conn net.Conn) error {

	head := make([]byte, Client_message_head_len)
	conn.SetReadDeadline(time.Now().Add(time.Second * 5))
	_, err := io.ReadFull(conn, head)
	if err != nil {
		return err
	}

	headers_len := byteOrder.Uint16(head[12:14])
	body_len := byteOrder.Uint32(head[14:18])

	m.Headers = make([]byte, headers_len)
	conn.SetReadDeadline(time.Now().Add(time.Second * 5))
	_, err = io.ReadFull(conn, m.Headers) //conn.Read(m.Headers)
	if err != nil {
		return err
	}

	m.Body = make([]byte, body_len)
	conn.SetReadDeadline(time.Now().Add(time.Second * 5))
	_, err = io.ReadFull(conn, m.Body)
	if err != nil {
		return err
	}

	m.Type = MessageType(head[0])
	m.ApplicationID = AppID(byteOrder.Uint16(head[2:4]))
	m.Timestamp = int64(byteOrder.Uint64(head[4:12])) // нужно ли читать таймстемп присланный клиентом???

	return nil
}

func (m *ClientMessage) ReadWithoutDeadline(conn net.Conn) error {

	head := make([]byte, Client_message_head_len)
	_, err := io.ReadFull(conn, head)
	if err != nil {
		return err
	}

	headers_len := byteOrder.Uint16(head[12:14])
	body_len := byteOrder.Uint32(head[14:18])

	m.Headers = make([]byte, headers_len)
	_, err = io.ReadFull(conn, m.Headers) //conn.Read(m.Headers)
	if err != nil {
		return err
	}

	m.Body = make([]byte, body_len)
	_, err = io.ReadFull(conn, m.Body)
	if err != nil {
		return err
	}

	m.Type = MessageType(head[0])
	m.ApplicationID = AppID(byteOrder.Uint16(head[2:4]))
	m.Timestamp = int64(byteOrder.Uint64(head[4:12])) // нужно ли читать таймстемп присланный клиентом???

	return nil
}

func (m *ClientMessage) Byte() ([]byte, error) {
	return EncodeClientMessage(m.Type, m.ApplicationID, m.Timestamp, m.Headers, m.Body)
}

func EncodeClientMessage(messagetype MessageType, appID AppID, timestamp int64, headers []byte, body []byte) ([]byte, error) {
	if len(headers) > 65535 {
		return nil, errors.New("headers overflows (len specified by uint16)")
	}
	if len(body) > 4294967295 {
		return nil, errors.New("body overflows (len specified by uint32)")
	}

	encoded := make([]byte, Client_message_head_len+len(headers)+len(body))

	encoded[0] = byte(messagetype)

	byteOrder.PutUint16(encoded[2:4], uint16(appID))
	byteOrder.PutUint64(encoded[4:12], uint64(timestamp)) // нужно ли читать таймстемп присланный клиентом???
	byteOrder.PutUint16(encoded[12:14], uint16(len(headers)))
	byteOrder.PutUint32(encoded[14:18], uint32(len(body)))

	copy(encoded[Client_message_head_len:Client_message_head_len+len(headers)], headers)
	copy(encoded[Client_message_head_len+len(headers):], body)

	return encoded, nil
}

func DecodeClientMessage(rawmessage []byte) (*ClientMessage, error) {
	if len(rawmessage) < Client_message_head_len {
		return nil, errors.New("weird data(raw message does not satisfy min head len)")
	}
	headers_len := byteOrder.Uint16(rawmessage[12:14])
	body_len := byteOrder.Uint32(rawmessage[14:18])
	if len(rawmessage) != Client_message_head_len+int(headers_len)+int(body_len) {
		return nil, errors.New("weird data(raw message is shorter/longer than specified head, body and headers lengths)")
	}

	m := &ClientMessage{
		Type:          MessageType(rawmessage[0]),
		ApplicationID: AppID(byteOrder.Uint16(rawmessage[2:4])),
		Timestamp:     int64(byteOrder.Uint64(rawmessage[4:12])),
		Headers:       rawmessage[Client_message_head_len : Client_message_head_len+headers_len],
		Body:          rawmessage[Client_message_head_len+headers_len:],
	}

	//m.Headers = make([]byte, headers_len)
	//copy(m.Headers, rawmessage[Client_message_head_len:Client_message_head_len+headers_len])

	//m.Body = make([]byte, body_len)
	//copy(m.Body, rawmessage[Client_message_head_len+headers_len:])
	return m, nil
}

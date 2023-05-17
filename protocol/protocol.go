package protocol

import (
	"encoding/binary"
)

type AppID uint16
type ConnUID uint32

var byteOrder = binary.BigEndian

type ErrorCode byte

const (
	ErrCodeNil                 ErrorCode = 0
	ErrCodeNotFound            ErrorCode = 1
	ErrCodeBadRequest          ErrorCode = 2
	ErrCodeInternalServerError ErrorCode = 3
	ErrForbidden               ErrorCode = 4
	ErrNotRegistered           ErrorCode = 5
)

func (ec ErrorCode) Byte() byte {
	return byte(ec)
}

func (ec ErrorCode) String() string {
	switch ec {
	case ErrCodeNotFound:
		return "notFound"
	case ErrCodeBadRequest:
		return "badRequest"
	case ErrCodeNil:
		return "nil"
	case ErrCodeInternalServerError:
		return "internalServerError"
	}
	return "unknown"
}

type MessageType byte

const (
	TypeInstall       MessageType = 1
	TypeConnect       MessageType = 2
	TypeText          MessageType = 3
	TypeError         MessageType = 4
	TypeDisconnection MessageType = 5
	TypeRegistration  MessageType = 6
	TypeTimestamp     MessageType = 7
	TypeCreate        MessageType = 8
	TypeUpdate        MessageType = 9
	TypeSettingsReq   MessageType = 10
	TypeRedirection   MessageType = 11

	TypeOAuthData MessageType = 12

	TypeOK MessageType = 14 // я хуй знает как назвать

)

func (mt MessageType) String() string {
	switch mt {
	case TypeInstall:
		return "Install"
	case TypeConnect:
		return "Connect"
	case TypeText:
		return "Text"
	case TypeError:
		return "Error"
	case TypeDisconnection:
		return "Disconnection"
	case TypeRegistration:
		return "Registration"
	case TypeTimestamp:
		return "Timestamp"
	case TypeCreate:
		return "Create"
	case TypeUpdate:
		return "Update"
	case TypeSettingsReq:
		return "SettingsReq"
	case TypeRedirection:
		return "Redirection"
	// case TypeToken:
	// 	return "Token"
	// case TypeAuthData:
	// 	return "AuthData"
	case TypeOK:
		return "OK"
		// case TypeGrant:
		// return "Grant"
	}
	return ""
}

// Протокол:
// client <--> appserver : 1 byte msgtype, 1 byte reserved, 2 bytes appID, 8 bytes timestamp, 2 bytes headers len, 4 bytes body len, дальше хедеры и тело
// appserver <--> app : 1 byte msgtype, 1 byte reserved, 4 bytes connUID, 8 bytes timestamp, 2 bytes headers len, 4 bytes body len, дальше хедеры и тело

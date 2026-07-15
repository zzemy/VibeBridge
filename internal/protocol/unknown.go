package protocol

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// HasUnknownFields recursively checks a decoded security-sensitive message.
func HasUnknownFields(message proto.Message) bool {
	if message == nil {
		return false
	}
	return hasUnknownFields(message.ProtoReflect())
}

func hasUnknownFields(message protoreflect.Message) bool {
	if !message.IsValid() {
		return true
	}
	if len(message.GetUnknown()) != 0 {
		return true
	}

	found := false
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		switch {
		case field.IsList() && field.Kind() == protoreflect.MessageKind:
			list := value.List()
			for index := 0; index < list.Len(); index++ {
				if hasUnknownFields(list.Get(index).Message()) {
					found = true
					return false
				}
			}
		case field.IsMap() && field.MapValue().Kind() == protoreflect.MessageKind:
			value.Map().Range(func(_ protoreflect.MapKey, item protoreflect.Value) bool {
				found = hasUnknownFields(item.Message())
				return !found
			})
		case field.Kind() == protoreflect.MessageKind:
			found = hasUnknownFields(value.Message())
		}
		return !found
	})
	return found
}

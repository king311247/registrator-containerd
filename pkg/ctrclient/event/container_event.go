package event

import "encoding/json"

type TaskEvent struct {
	ContainerId string `json:"container_id"`
}

func (event *TaskEvent) TaskEventUnmarshal(eventData []byte) error {
	return json.Unmarshal(eventData, event)
}

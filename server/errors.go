package main

import "fmt"

type ServiceError struct {
	Op  string
	Err error
}

func (e *ServiceError) Error() string {
	return fmt.Sprintf("%s fail: %v", e.Op, e.Err)
}

func (e *ServiceError) Unwrap() error { return e.Err }

func Fail(op string, err error) error {
	if err == nil {
		return nil
	}
	return &ServiceError{Op: op, Err: err}
}

func Wrap(op string, err error) error {
	return Fail(op, err)
}

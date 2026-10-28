package main

import "errors"

// greet 是纯函数：不碰 flag、不碰 stdout，只做输入校验和字符串拼接。
func greet(name string) (string, error) {
	if name == "" {
		return "", errors.New("-name must not be empty")
	}
	return "hello, " + name, nil
}

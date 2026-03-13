#!/bin/bash

curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer sk-your-api-key-1" \
  -d '{
  "model": "gpt-4",
  "stream": false,
  "max_tokens": 4096,
  "temperature": 0.7,
  "tools": [
    {
      "type": "function",
      "function": {
        "name": "get_weather",
        "description": "获取指定城市的天气信息",
        "parameters": {
          "type": "object",
          "properties": {
            "city": {
              "type": "string",
              "description": "城市名称，例如：北京、上海"
            },
            "unit": {
              "type": "string",
              "enum": ["celsius", "fahrenheit"],
              "description": "温度单位"
            }
          },
          "required": ["city"]
        }
      }
    },
    {
      "type": "function",
      "function": {
        "name": "search_knowledge",
        "description": "在知识库中搜索相关信息",
        "parameters": {
          "type": "object",
          "properties": {
            "query": {
              "type": "string",
              "description": "搜索关键词"
            },
            "top_k": {
              "type": "integer",
              "description": "返回结果数量"
            }
          },
          "required": ["query"]
        }
      }
    }
  ],
  "tool_choice": "auto",
  "messages": [
    {
      "role": "system",
      "content": "你是一个有用的助手，可以使用工具来获取实时信息。"
    },
    {
      "role": "user",
      "content": "北京今天天气怎么样？"
    },
    {
      "role": "assistant",
      "content": null,
      "tool_calls": [
        {
          "id": "call_abc123",
          "type": "function",
          "function": {
            "name": "get_weather",
            "arguments": "{\"city\": \"北京\", \"unit\": \"celsius\"}"
          }
        }
      ]
    },
    {
      "role": "tool",
      "tool_call_id": "call_abc123",
      "name": "get_weather",
      "content": "{\"city\": \"北京\", \"temperature\": 22, \"unit\": \"celsius\", \"condition\": \"晴\", \"humidity\": 45}"
    },
    {
      "role": "assistant",
      "content": "北京今天天气晴朗，气温22°C，湿度45%，非常适合出行。"
    },
    {
      "role": "user",
      "content": "帮我再查一下上海的天气，顺便搜索一下上海有什么好玩的地方。"
    }
  ]
}'
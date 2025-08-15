cat transcript.txt | llm -m openrouter/openai/gpt-5-mini "What are the top ten topics of this sermon? Use topics from the following list: $(cat topics.txt)"

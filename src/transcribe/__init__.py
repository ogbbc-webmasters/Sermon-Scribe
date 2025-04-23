import os
import assemblyai as aai
from dotenv import load_dotenv

load_dotenv()

aai.settings.api_key = os.getenv('API_KEY')

transcriber = aai.Transcriber()

transcript = transcriber.transcribe(os.getenv('FILE_URL'))

# After the transcription is complete, the text is printed out to the console.
print(transcript.text)

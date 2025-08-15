import os
import assemblyai as aai
from dotenv import load_dotenv

load_dotenv()

aai.settings.api_key = os.getenv('API_KEY')

transcriber = aai.Transcriber()
config = aai.TranscriptionConfig(
    summarization=True,
    summary_model=aai.SummarizationModel.informative,
    summary_type=aai.SummarizationType.bullets_verbose,
)

transcript = transcriber.transcribe(os.getenv('FILE_URL'), config)

print('Transcript ID:', transcript.id)
with open('summary.txt', 'w') as f:
    f.write(transcript.summary)
with open('transcript.txt', 'w') as f:
    f.write(transcript.text)

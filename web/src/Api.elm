module Api exposing
    ( AICost
    , PipelineEvent(..)
    , Sermon
    , TranscriptSegment
    , TranscriptWord
    , TranscriptionMetadata
    , deleteSermon
    , fetchSermons
    , pipelineEventDecoder
    , retryProcessing
    , saveScriptures
    , scriptureOptions
    , sermonDecoder
    , uploadSermon
    , uploadTracker
    )

{-| Server API: sermon state, pipeline events, and HTTP requests.
-}

import Dict
import File exposing (File)
import Http
import Json.Decode as Decode exposing (Decoder)
import Json.Encode as Encode


type alias Sermon =
    { id : String
    , originalFilename : String
    , uploadedAt : String
    , uploadedBy : Maybe String
    , stage : String
    , status : String
    , progress : Int
    , error : Maybe String
    , normalizationGateAdjustment : Int
    , normalizationVolumeAdjustment : Int
    , normalizationReviewed : Bool
    , transcript : Maybe String
    , title : Maybe String
    , titleGenerated : Maybe Bool
    , titleReasoning : Maybe String
    , speaker : Maybe String
    , oldTestamentReading : Maybe String
    , newTestamentReading : Maybe String
    , scriptures : List String
    , scriptureOptions : List String
    , topics : List String
    , topicScores : List ( String, Float )
    , transcriptionMetadata : Maybe TranscriptionMetadata
    , sourceTranscriptionMetadata : Maybe TranscriptionMetadata
    , editingDuration : Maybe Float
    , playbackVersion : String
    , aiCosts : List AICost
    }


type alias AICost =
    { task : String
    , model : String
    , calls : Int
    , costUsd : Float
    , unknownCosts : Int
    }


type alias TranscriptionMetadata =
    { language : Maybe String
    , duration : Maybe Float
    , segments : List TranscriptSegment
    , words : List TranscriptWord
    }


type alias TranscriptSegment =
    { start : Float
    , end : Float
    , text : String
    , speaker : Maybe Int
    }


type alias TranscriptWord =
    { word : String
    , start : Float
    , end : Float
    , speaker : Maybe Int
    , speakerLabel : Maybe String
    , confidence : Maybe Float
    }


type PipelineEvent
    = PipelineSnapshot (List Sermon)
    | PipelineUpdate Sermon
    | PipelineDeleted String


sermonDecoder : Decoder Sermon
sermonDecoder =
    Decode.map4
        (\sermon gate volume reviewed ->
            { sermon
                | normalizationGateAdjustment = gate
                , normalizationVolumeAdjustment = volume
                , normalizationReviewed = reviewed
                , transcript = Nothing
                , title = Nothing
                , titleGenerated = Nothing
                , titleReasoning = Nothing
                , speaker = Nothing
                , oldTestamentReading = Nothing
                , newTestamentReading = Nothing
                , scriptures = []
                , scriptureOptions = []
                , topics = []
                , topicScores = []
                , transcriptionMetadata = Nothing
            }
        )
        (Decode.map8
            (\id originalFilename uploadedAt uploadedBy stage status progress error ->
                Sermon id originalFilename uploadedAt uploadedBy stage status progress error 0 0 False Nothing Nothing Nothing Nothing Nothing Nothing Nothing [] [] [] [] Nothing Nothing Nothing "" []
            )
            (Decode.field "id" Decode.string)
            (Decode.field "original_filename" Decode.string)
            (Decode.field "uploaded_at" Decode.string)
            (Decode.field "uploaded_by" (Decode.nullable Decode.string))
            (Decode.field "stage" Decode.string)
            (Decode.field "status" Decode.string)
            (Decode.field "progress" Decode.int)
            (Decode.field "error" (Decode.nullable Decode.string))
        )
        (Decode.field "normalization_gate_adjustment" Decode.int)
        (Decode.field "normalization_volume_adjustment" Decode.int)
        (Decode.oneOf [ Decode.field "normalization_reviewed" Decode.bool, Decode.succeed False ])
        |> Decode.andThen decodeMetadata
        |> Decode.andThen
            (\sermon ->
                Decode.map (\costs -> { sermon | aiCosts = costs })
                    (Decode.field "ai_costs" (Decode.list aiCostDecoder))
            )


aiCostDecoder : Decoder AICost
aiCostDecoder =
    Decode.map5 AICost
        (Decode.field "task" Decode.string)
        (Decode.field "model" Decode.string)
        (Decode.field "calls" Decode.int)
        (Decode.field "cost_usd" Decode.float)
        (Decode.field "unknown_costs" Decode.int)


decodeMetadata : Sermon -> Decoder Sermon
decodeMetadata sermon =
    Decode.map8
        (\transcript title generated reasoning speaker oldTestamentReading newTestamentReading scriptures ->
            { sermon
                | transcript = transcript
                , title = title
                , titleGenerated = generated
                , titleReasoning = reasoning
                , speaker = speaker
                , oldTestamentReading = oldTestamentReading
                , newTestamentReading = newTestamentReading
                , scriptures = scriptures
            }
        )
        (Decode.maybe (Decode.field "transcript" Decode.string))
        (Decode.maybe (Decode.field "title" Decode.string))
        (Decode.maybe (Decode.field "title_generated" Decode.bool))
        (Decode.maybe (Decode.field "title_reasoning" Decode.string))
        (Decode.maybe (Decode.field "speaker" Decode.string))
        (Decode.maybe (Decode.field "old_testament_reading" Decode.string))
        (Decode.maybe (Decode.field "new_testament_reading" Decode.string))
        (Decode.oneOf [ Decode.field "scriptures" (Decode.list Decode.string), Decode.succeed [] ])
        |> Decode.andThen
            (\decodedSermon ->
                Decode.map2
                    (\options topics ->
                        { decodedSermon
                            | scriptureOptions = options
                            , topics = topics
                        }
                    )
                    (Decode.oneOf [ Decode.field "scripture_options" (Decode.list Decode.string), Decode.succeed [] ])
                    (Decode.oneOf [ Decode.field "topics" (Decode.list Decode.string), Decode.succeed [] ])
                    |> Decode.andThen
                        (\withTopics ->
                            Decode.map
                                (\scores -> { withTopics | topicScores = Dict.toList scores })
                                (Decode.oneOf [ Decode.field "topic_scores" (Decode.dict Decode.float), Decode.succeed Dict.empty ])
                        )
            )
        |> Decode.andThen
            (\decodedSermon ->
                Decode.map4
                    (\metadata sourceMetadata duration playback -> { decodedSermon | transcriptionMetadata = metadata, sourceTranscriptionMetadata = sourceMetadata, editingDuration = duration, playbackVersion = playback })
                    (Decode.maybe (Decode.field "transcription_metadata" transcriptionMetadataDecoder))
                    (Decode.maybe (Decode.field "source_transcription_metadata" transcriptionMetadataDecoder))
                    (Decode.maybe (Decode.field "editing_duration" Decode.float))
                    (Decode.oneOf [ Decode.field "playback_version" Decode.string, Decode.succeed "" ])
            )


transcriptionMetadataDecoder : Decoder TranscriptionMetadata
transcriptionMetadataDecoder =
    Decode.map4 TranscriptionMetadata
        (Decode.maybe (Decode.field "language" Decode.string))
        (Decode.maybe (Decode.field "duration" Decode.float))
        (Decode.oneOf [ Decode.field "segments" (Decode.list transcriptSegmentDecoder), Decode.succeed [] ])
        (Decode.oneOf [ Decode.field "words" (Decode.list transcriptWordDecoder), Decode.succeed [] ])


transcriptSegmentDecoder : Decoder TranscriptSegment
transcriptSegmentDecoder =
    Decode.map4 TranscriptSegment
        (Decode.field "start" Decode.float)
        (Decode.field "end" Decode.float)
        (Decode.field "text" Decode.string)
        (Decode.maybe (Decode.field "speaker" Decode.int))


transcriptWordDecoder : Decoder TranscriptWord
transcriptWordDecoder =
    Decode.map6 TranscriptWord
        (Decode.field "word" Decode.string)
        (Decode.field "start" Decode.float)
        (Decode.field "end" Decode.float)
        (Decode.maybe (Decode.field "speaker" Decode.int))
        (Decode.maybe (Decode.field "speaker_label" Decode.string))
        (Decode.maybe (Decode.field "confidence" Decode.float))


pipelineEventDecoder : Decoder PipelineEvent
pipelineEventDecoder =
    Decode.field "event" Decode.string
        |> Decode.andThen
            (\eventName ->
                case eventName of
                    "snapshot" ->
                        Decode.field "data" (Decode.list sermonDecoder)
                            |> Decode.map PipelineSnapshot

                    "stage_started" ->
                        pipelineUpdateDecoder

                    "progress" ->
                        pipelineUpdateDecoder

                    "stage_completed" ->
                        pipelineUpdateDecoder

                    "failed" ->
                        pipelineUpdateDecoder

                    "deleted" ->
                        Decode.at [ "data", "id" ] Decode.string
                            |> Decode.map PipelineDeleted

                    _ ->
                        Decode.fail ("unknown pipeline event: " ++ eventName)
            )


pipelineUpdateDecoder : Decoder PipelineEvent
pipelineUpdateDecoder =
    Decode.field "data" sermonDecoder
        |> Decode.map PipelineUpdate


scriptureOptions : Sermon -> List String
scriptureOptions sermon =
    let
        readings =
            List.filterMap identity [ sermon.oldTestamentReading, sermon.newTestamentReading ]

        available =
            if List.isEmpty sermon.scriptureOptions then
                sermon.scriptures

            else
                sermon.scriptureOptions

        additional =
            List.filter (\reference -> not (List.member reference readings)) available
    in
    List.take 6 (readings ++ additional)


{-| Tracker id for `Http.track`ing upload progress.
-}
uploadTracker : String
uploadTracker =
    "sermon-upload"


fetchSermons : (Result Http.Error (List Sermon) -> msg) -> Cmd msg
fetchSermons toMsg =
    Http.get
        { url = "/api/sermons"
        , expect = Http.expectJson toMsg (Decode.list sermonDecoder)
        }


uploadSermon : (Result Http.Error Sermon -> msg) -> File -> Cmd msg
uploadSermon toMsg file =
    Http.request
        { method = "POST"
        , headers = []
        , url = "/api/sermons"
        , body = Http.multipartBody [ Http.filePart "file" file ]
        , expect = Http.expectJson toMsg sermonDecoder
        , timeout = Nothing
        , tracker = Just uploadTracker
        }


deleteSermon : (Result Http.Error () -> msg) -> String -> Cmd msg
deleteSermon toMsg id =
    Http.request
        { method = "DELETE"
        , headers = []
        , url = "/api/sermons/" ++ id
        , body = Http.emptyBody
        , expect = Http.expectWhatever toMsg
        , timeout = Nothing
        , tracker = Nothing
        }


retryProcessing : (Result Http.Error Sermon -> msg) -> String -> String -> Cmd msg
retryProcessing toMsg id part =
    Http.post
        { url =
            "/api/sermons/"
                ++ id
                ++ "/retry"
                ++ (if part == "" then
                        ""

                    else
                        "/" ++ part
                   )
        , body = Http.emptyBody
        , expect = Http.expectJson toMsg sermonDecoder
        }


saveScriptures : (Result Http.Error Sermon -> msg) -> String -> List String -> Cmd msg
saveScriptures toMsg id scriptures =
    Http.request
        { method = "PUT"
        , headers = []
        , url = "/api/sermons/" ++ id ++ "/scriptures"
        , body =
            Http.jsonBody
                (Encode.object
                    [ ( "scriptures", Encode.list Encode.string scriptures ) ]
                )
        , expect = Http.expectJson toMsg sermonDecoder
        , timeout = Nothing
        , tracker = Nothing
        }

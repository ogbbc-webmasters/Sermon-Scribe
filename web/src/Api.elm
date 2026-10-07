module Api exposing
    ( PipelineEvent(..)
    , Sermon
    , deleteSermon
    , fetchSermons
    , pipelineEventDecoder
    , retryProcessing
    , sermonDecoder
    , uploadSermon
    , uploadTracker
    )

{-| Server API: sermon state, pipeline events, and HTTP requests.
-}

import File exposing (File)
import Http
import Json.Decode as Decode exposing (Decoder)
import Dict


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
    , scriptures : List String
    , topics : List String
    , topicScores : List ( String, Float )
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
                , scriptures = []
                , topics = []
                , topicScores = []
            }
        )
        (Decode.map8
            (\id originalFilename uploadedAt uploadedBy stage status progress error ->
                Sermon id originalFilename uploadedAt uploadedBy stage status progress error 0 0 False Nothing Nothing Nothing Nothing Nothing [] [] []
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


decodeMetadata : Sermon -> Decoder Sermon
decodeMetadata sermon =
    Decode.map7
        (\transcript title generated reasoning speaker scriptures topics ->
            { sermon
                | transcript = transcript
                , title = title
                , titleGenerated = generated
                , titleReasoning = reasoning
                , speaker = speaker
                , scriptures = scriptures
                , topics = topics
            }
        )
        (Decode.maybe (Decode.field "transcript" Decode.string))
        (Decode.maybe (Decode.field "title" Decode.string))
        (Decode.maybe (Decode.field "title_generated" Decode.bool))
        (Decode.maybe (Decode.field "title_reasoning" Decode.string))
        (Decode.maybe (Decode.field "speaker" Decode.string))
        (Decode.oneOf [ Decode.field "scriptures" (Decode.list Decode.string), Decode.succeed [] ])
        (Decode.oneOf [ Decode.field "topics" (Decode.list Decode.string), Decode.succeed [] ])
        |> Decode.andThen
            (\decodedSermon ->
                Decode.map
                    (\scores -> { decodedSermon | topicScores = Dict.toList scores })
                    (Decode.oneOf [ Decode.field "topic_scores" (Decode.dict Decode.float), Decode.succeed Dict.empty ])
            )


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
        { url = "/api/sermons/" ++ id ++ "/retry/" ++ part
        , body = Http.emptyBody
        , expect = Http.expectJson toMsg sermonDecoder
        }

module Api exposing
    ( PipelineEvent(..)
    , Sermon
    , deleteSermon
    , fetchSermons
    , pipelineEventDecoder
    , retrySermon
    , rerunNormalization
    , sermonDecoder
    , uploadSermon
    , uploadTracker
    )

{-| Server API: sermon state, pipeline events, and HTTP requests.
-}

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
    }


type PipelineEvent
    = PipelineSnapshot (List Sermon)
    | PipelineUpdate Sermon
    | PipelineDeleted String


sermonDecoder : Decoder Sermon
sermonDecoder =
    Decode.map3
        (\sermon gate volume ->
            { sermon
                | normalizationGateAdjustment = gate
                , normalizationVolumeAdjustment = volume
            }
        )
        (Decode.map8
            (\id originalFilename uploadedAt uploadedBy stage status progress error ->
                Sermon id originalFilename uploadedAt uploadedBy stage status progress error 0 0
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


retrySermon : (Result Http.Error Sermon -> msg) -> String -> Cmd msg
retrySermon toMsg id =
    Http.request
        { method = "POST"
        , headers = []
        , url = "/api/sermons/" ++ id ++ "/retry"
        , body = Http.emptyBody
        , expect = Http.expectJson toMsg sermonDecoder
        , timeout = Nothing
        , tracker = Nothing
        }


rerunNormalization : (Result Http.Error Sermon -> msg) -> String -> String -> Cmd msg
rerunNormalization toMsg id adjustment =
    Http.post
        { url = "/api/sermons/" ++ id ++ "/normalize"
        , body =
            Http.jsonBody
                (Encode.object [ ( "adjustment", Encode.string adjustment ) ])
        , expect = Http.expectJson toMsg sermonDecoder
        }

module Api exposing
    ( PipelineEvent(..)
    , Region
    , Sermon
    , Timeline
    , Waveform
    , analyze
    , applyEdits
    , approveEdit
    , deleteSermon
    , fetchSermons
    , pipelineEventDecoder
    , rerunNormalization
    , retrySermon
    , reviewNormalization
    , sermonDecoder
    , uploadSermon
    , uploadTracker
    , waveform
    )

{-| Server API: sermon state, pipeline events, and HTTP requests.
-}

import File exposing (File)
import Http
import Json.Decode as Decode exposing (Decoder)
import Json.Encode as Encode
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
    , appliedRegions : Maybe (List Region)
    , editApproved : Bool
    , transcript : Maybe String
    , title : Maybe String
    , titleGenerated : Maybe Bool
    , titleReasoning : Maybe String
    , speaker : Maybe String
    , scriptures : List String
    , topics : List String
    , topicsReasoning : List ( String, String )
    , topicScores : List ( String, Float )
    }


type alias Region =
    { start : Float, end : Float, regionType : String, keep : Bool }


type alias Waveform =
    { duration : Float, samplesPerSecond : Float, samples : List Float }


type alias Timeline =
    { duration : Float, regions : List Region }


regionDecoder : Decoder Region
regionDecoder =
    Decode.map4 Region (Decode.field "start" Decode.float) (Decode.field "end" Decode.float) (Decode.field "type" Decode.string) (Decode.field "keep" Decode.bool)


regionEncoder : Region -> Encode.Value
regionEncoder region =
    Encode.object [ ( "start", Encode.float region.start ), ( "end", Encode.float region.end ), ( "type", Encode.string region.regionType ), ( "keep", Encode.bool region.keep ) ]


type PipelineEvent
    = PipelineSnapshot (List Sermon)
    | PipelineUpdate Sermon
    | PipelineDeleted String


sermonDecoder : Decoder Sermon
sermonDecoder =
    Decode.map6
        (\sermon gate volume reviewed applied approved ->
            { sermon
                | normalizationGateAdjustment = gate
                , normalizationVolumeAdjustment = volume
                , normalizationReviewed = reviewed
                , appliedRegions = applied
                , editApproved = approved
                , transcript = Nothing
                , title = Nothing
                , titleGenerated = Nothing
                , titleReasoning = Nothing
                , speaker = Nothing
                , scriptures = []
                , topics = []
                , topicsReasoning = []
                , topicScores = []
            }
        )
        (Decode.map8
            (\id originalFilename uploadedAt uploadedBy stage status progress error ->
                Sermon id originalFilename uploadedAt uploadedBy stage status progress error 0 0 False Nothing False Nothing Nothing Nothing Nothing Nothing [] [] [] []
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
        (Decode.maybe (Decode.field "applied_regions" (Decode.nullable (Decode.list regionDecoder))) |> Decode.map (Maybe.withDefault Nothing))
        (Decode.oneOf [ Decode.field "edit_approved" Decode.bool, Decode.succeed False ])
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
                Decode.map2
                    (\scores reasoning -> { decodedSermon | topicScores = Dict.toList scores, topicsReasoning = Dict.toList reasoning })
                    (Decode.oneOf [ Decode.field "topic_scores" (Decode.dict Decode.float), Decode.succeed Dict.empty ])
                    (Decode.oneOf [ Decode.field "topics_reasoning" (Decode.dict Decode.string), Decode.succeed Dict.empty ])
            )


waveform : (Result Http.Error Waveform -> msg) -> String -> Cmd msg
waveform toMsg id =
    Http.get { url = "/api/sermons/" ++ id ++ "/waveform", expect = Http.expectJson toMsg (Decode.map3 Waveform (Decode.field "duration" Decode.float) (Decode.field "samples_per_second" Decode.float) (Decode.field "samples" (Decode.list Decode.float))) }


analyze : (Result Http.Error Timeline -> msg) -> String -> Cmd msg
analyze toMsg id =
    Http.post { url = "/api/sermons/" ++ id ++ "/analyze", body = Http.emptyBody, expect = Http.expectJson toMsg (Decode.map2 Timeline (Decode.field "duration" Decode.float) (Decode.field "regions" (Decode.list regionDecoder))) }


applyEdits : (Result Http.Error Sermon -> msg) -> String -> List Region -> Cmd msg
applyEdits toMsg id regions =
    Http.post { url = "/api/sermons/" ++ id ++ "/apply-edits", body = Http.jsonBody (Encode.object [ ( "regions", Encode.list regionEncoder regions ) ]), expect = Http.expectJson toMsg sermonDecoder }


approveEdit : (Result Http.Error Sermon -> msg) -> String -> Cmd msg
approveEdit toMsg id =
    Http.post { url = "/api/sermons/" ++ id ++ "/approve-edit", body = Http.emptyBody, expect = Http.expectJson toMsg sermonDecoder }


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


reviewNormalization : (Result Http.Error Sermon -> msg) -> String -> Cmd msg
reviewNormalization toMsg id =
    Http.post
        { url = "/api/sermons/" ++ id ++ "/review-normalization"
        , body = Http.emptyBody
        , expect = Http.expectJson toMsg sermonDecoder
        }

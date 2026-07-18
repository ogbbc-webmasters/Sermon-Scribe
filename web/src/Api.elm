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
    , retrySermon
    , rerunNormalization
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
    , appliedRegions : Maybe (List Region)
    , editApproved : Bool
    }

type alias Region = { start : Float, end : Float, regionType : String, keep : Bool }
type alias Waveform = { duration : Float, samplesPerSecond : Float, samples : List Float }
type alias Timeline = { duration : Float, regions : List Region }

regionDecoder : Decoder Region
regionDecoder =
    Decode.map4 Region (Decode.field "start" Decode.float) (Decode.field "end" Decode.float) (Decode.field "type" Decode.string) (Decode.field "keep" Decode.bool)

regionEncoder : Region -> Encode.Value
regionEncoder region = Encode.object [ ( "start", Encode.float region.start ), ( "end", Encode.float region.end ), ( "type", Encode.string region.regionType ), ( "keep", Encode.bool region.keep ) ]


type PipelineEvent
    = PipelineSnapshot (List Sermon)
    | PipelineUpdate Sermon
    | PipelineDeleted String


sermonDecoder : Decoder Sermon
sermonDecoder =
    Decode.map5
        (\sermon gate volume applied approved ->
            { sermon
                | normalizationGateAdjustment = gate
                , normalizationVolumeAdjustment = volume
                , appliedRegions = applied
                , editApproved = approved
            }
        )
        (Decode.map8
            (\id originalFilename uploadedAt uploadedBy stage status progress error ->
                Sermon id originalFilename uploadedAt uploadedBy stage status progress error 0 0 Nothing False
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
        (Decode.maybe (Decode.field "applied_regions" (Decode.nullable (Decode.list regionDecoder))) |> Decode.map (Maybe.withDefault Nothing))
        (Decode.oneOf [ Decode.field "edit_approved" Decode.bool, Decode.succeed False ])

waveform : (Result Http.Error Waveform -> msg) -> String -> Cmd msg
waveform toMsg id = Http.get { url = "/api/sermons/" ++ id ++ "/waveform", expect = Http.expectJson toMsg (Decode.map3 Waveform (Decode.field "duration" Decode.float) (Decode.field "samples_per_second" Decode.float) (Decode.field "samples" (Decode.list Decode.float))) }

analyze : (Result Http.Error Timeline -> msg) -> String -> Cmd msg
analyze toMsg id = Http.post { url = "/api/sermons/" ++ id ++ "/analyze", body = Http.emptyBody, expect = Http.expectJson toMsg (Decode.map2 Timeline (Decode.field "duration" Decode.float) (Decode.field "regions" (Decode.list regionDecoder))) }

applyEdits : (Result Http.Error Sermon -> msg) -> String -> List Region -> Cmd msg
applyEdits toMsg id regions = Http.post { url = "/api/sermons/" ++ id ++ "/apply-edits", body = Http.jsonBody (Encode.object [ ( "regions", Encode.list regionEncoder regions ) ]), expect = Http.expectJson toMsg sermonDecoder }

approveEdit : (Result Http.Error Sermon -> msg) -> String -> Cmd msg
approveEdit toMsg id = Http.post { url = "/api/sermons/" ++ id ++ "/approve-edit", body = Http.emptyBody, expect = Http.expectJson toMsg sermonDecoder }


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

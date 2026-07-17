module Api exposing (Sermon, deleteSermon, fetchSermons, uploadSermon, uploadTracker)

{-| Server API: the Sermon type, its JSON decoder, and the HTTP requests.
-}

import File exposing (File)
import Http
import Json.Decode as Decode exposing (Decoder)


type alias Sermon =
    { id : String
    , originalFilename : String
    , uploadedAt : String
    , uploadedBy : Maybe String
    , stage : String
    , status : String
    }


sermonDecoder : Decoder Sermon
sermonDecoder =
    Decode.map6 Sermon
        (Decode.field "id" Decode.string)
        (Decode.field "original_filename" Decode.string)
        (Decode.field "uploaded_at" Decode.string)
        (Decode.field "uploaded_by" (Decode.nullable Decode.string))
        (Decode.field "stage" Decode.string)
        (Decode.field "status" Decode.string)


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


uploadSermon : (Result Http.Error () -> msg) -> File -> Cmd msg
uploadSermon toMsg file =
    Http.request
        { method = "POST"
        , headers = []
        , url = "/api/sermons"
        , body = Http.multipartBody [ Http.filePart "file" file ]
        , expect = Http.expectWhatever toMsg
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

module Types exposing (Model, Msg(..), SermonList(..), UploadState(..))

import Api exposing (Sermon)
import Browser
import Browser.Navigation as Navigation
import Dict exposing (Dict)
import File exposing (File)
import Http
import Json.Decode as Decode
import Set exposing (Set)
import Time
import Url exposing (Url)


type SermonList
    = Loading
    | Loaded (List Sermon)
    | LoadFailed


type UploadState
    = Idle
    | Uploading Float
    | UploadFailed String


type alias Model =
    { sermons : SermonList
    , selectedSermon : Maybe String
    , navigationKey : Navigation.Key
    , transcriptSearch : String
    , transcriptMatch : Int
    , transcriptCopyStatus : Maybe Bool
    , hasPipelineSnapshot : Bool
    , upload : UploadState
    , confirmingDelete : Maybe Sermon
    , deleting : Set String
    , deletedSermons : Set String
    , deleteError : Maybe String
    , retrying : Set String
    , regenerating : Dict String String
    , retryError : Maybe String
    , scriptureDrafts : Dict String (Set String)
    , scriptureSaving : Set String
    , scriptureSaveErrors : Set String
    , zone : Time.Zone
    }


type Msg
    = NoOp
    | UrlRequested Browser.UrlRequest
    | UrlChanged Url
    | GotZone Time.Zone
    | GotSermons (Result Http.Error (List Sermon))
    | PipelineEventReceived Decode.Value
    | FilePicked File
    | UploadProgress Http.Progress
    | UploadFinished (Result Http.Error Sermon)
    | RetryProcessing Sermon String
    | RetryFinished Sermon (Result Http.Error Sermon)
    | ToggleScripture String String
    | ScriptureSaveFinished String (List String) (Result Http.Error Sermon)
    | RetryScriptureSave Sermon
    | OpenSermon Sermon
    | SearchTranscript String
    | SelectTranscriptMatch Int
    | CopyTranscript String
    | TranscriptCopied Bool
    | AskDelete Sermon
    | CancelDelete
    | ConfirmDelete Sermon
    | DeleteFinished String (Result Http.Error ())

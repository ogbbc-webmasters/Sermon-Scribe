module Main exposing (main)

import Api
import Browser
import Http
import Task
import Time
import Types exposing (Model, Msg(..), SermonList(..), UploadState(..))
import View


main : Program () Model Msg
main =
    Browser.element
        { init = init
        , update = update
        , view = View.view
        , subscriptions = subscriptions
        }


init : () -> ( Model, Cmd Msg )
init _ =
    ( { sermons = Loading
      , upload = Idle
      , confirmingDelete = Nothing
      , deleteError = Nothing
      , zone = Time.utc
      }
    , Cmd.batch [ Api.fetchSermons GotSermons, Task.perform GotZone Time.here ]
    )



-- UPDATE


update : Msg -> Model -> ( Model, Cmd Msg )
update msg model =
    case msg of
        GotZone zone ->
            ( { model | zone = zone }, Cmd.none )

        GotSermons (Ok sermons) ->
            ( { model | sermons = Loaded sermons }, Cmd.none )

        GotSermons (Err _) ->
            ( { model | sermons = LoadFailed }, Cmd.none )

        FilePicked file ->
            ( { model | upload = Uploading 0 }
            , Api.uploadSermon UploadFinished file
            )

        UploadProgress progress ->
            case progress of
                Http.Sending sent ->
                    ( { model | upload = Uploading (Http.fractionSent sent) }
                    , Cmd.none
                    )

                Http.Receiving _ ->
                    ( model, Cmd.none )

        UploadFinished (Ok ()) ->
            ( { model | upload = Idle, deleteError = Nothing }, Api.fetchSermons GotSermons )

        UploadFinished (Err err) ->
            ( { model | upload = UploadFailed (uploadErrorMessage err) }
            , Cmd.none
            )

        AskDelete sermon ->
            ( { model | confirmingDelete = Just sermon }, Cmd.none )

        CancelDelete ->
            ( { model | confirmingDelete = Nothing }, Cmd.none )

        ConfirmDelete sermon ->
            ( { model | confirmingDelete = Nothing, deleteError = Nothing }
            , Api.deleteSermon DeleteFinished sermon.id
            )

        DeleteFinished (Ok ()) ->
            ( { model | deleteError = Nothing }, Api.fetchSermons GotSermons )

        DeleteFinished (Err _) ->
            -- Refresh anyway so the list matches the server; the sermon
            -- reappears, and the error explains why.
            ( { model | deleteError = Just "Could not delete. Please try again." }
            , Api.fetchSermons GotSermons
            )


subscriptions : Model -> Sub Msg
subscriptions model =
    case model.upload of
        Uploading _ ->
            Http.track Api.uploadTracker UploadProgress

        _ ->
            Sub.none


uploadErrorMessage : Http.Error -> String
uploadErrorMessage err =
    case err of
        Http.BadStatus 413 ->
            "That file is too large. The limit is 2 GB."

        Http.NetworkError ->
            "The connection was interrupted. Please try again."

        Http.Timeout ->
            "The upload took too long. Please try again."

        _ ->
            "Something went wrong with the upload. Please try again."
